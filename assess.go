package main

import (
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
)

// partialSpec describes one bell partial in the fixed reporting order.
type partialSpec struct {
	name      string
	multiple  float64 // target frequency = multiple * hum
	tolerance float64 // acceptable deviation in cents, ±, boundary inclusive
}

// partialSpecs is the fixed assessment order: hum, prime, tierce, quint, nominal.
var partialSpecs = []partialSpec{
	{name: "hum", multiple: 1.0, tolerance: 5},
	{name: "prime", multiple: 2.0, tolerance: 8},
	{name: "tierce", multiple: 2.4, tolerance: 8},
	{name: "quint", multiple: 3.0, tolerance: 8},
	{name: "nominal", multiple: 4.0, tolerance: 8},
}

const (
	humMinHz = 20.0
	humMaxHz = 1000.0
)

type partialResult struct {
	Name           string `json:"name"`
	TargetHz       string `json:"target_hz"`
	MeasuredHz     string `json:"measured_hz"`
	DeviationCents string `json:"deviation_cents"`
	ToleranceCents int    `json:"tolerance_cents"`
	Result         string `json:"result"`
}

type summaryResult struct {
	Verdict   string   `json:"verdict"`
	OutOfTune []string `json:"out_of_tune"`
}

type assessResponse struct {
	Partials []partialResult `json:"partials"`
	Summary  summaryResult   `json:"summary"`
}

type fieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type errorResponse struct {
	Error  string       `json:"error"`
	Fields []fieldError `json:"fields"`
}

// deviationCents returns 1200*log2(measured/target); inputs must be positive.
func deviationCents(measured, target float64) float64 {
	return 1200 * math.Log2(measured/target)
}

// withinTolerance reports whether cents lies inside ±tolerance.
// The comparison uses the unrounded value and the boundary counts as pass.
func withinTolerance(cents, tolerance float64) bool {
	return math.Abs(cents) <= tolerance
}

// round2 rounds half away from zero (四舍五入) to two decimal places.
func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

// formatHz renders a frequency rounded to two decimals.
func formatHz(x float64) string {
	return strconv.FormatFloat(round2(x), 'f', 2, 64)
}

// formatCents renders a signed deviation with two decimals; a value that
// rounds to zero is shown as "0.00" (never "-0.00").
func formatCents(cents float64) string {
	r := round2(cents)
	if r == 0 {
		return "0.00"
	}
	if r > 0 {
		return "+" + strconv.FormatFloat(r, 'f', 2, 64)
	}
	return strconv.FormatFloat(r, 'f', 2, 64)
}

// assess evaluates the five partials against their targets. Verdicts are
// computed from unrounded cent deviations; rounding is display-only.
func assess(measured map[string]float64) assessResponse {
	resp := assessResponse{Summary: summaryResult{OutOfTune: []string{}}}
	hum := measured["hum"]
	for _, spec := range partialSpecs {
		m := measured[spec.name]
		target := spec.multiple * hum
		cents := deviationCents(m, target)
		result := "pass"
		if !withinTolerance(cents, spec.tolerance) {
			result = "fail"
			resp.Summary.OutOfTune = append(resp.Summary.OutOfTune, spec.name)
		}
		resp.Partials = append(resp.Partials, partialResult{
			Name:           spec.name,
			TargetHz:       formatHz(target),
			MeasuredHz:     formatHz(m),
			DeviationCents: formatCents(cents),
			ToleranceCents: int(spec.tolerance),
			Result:         result,
		})
	}
	if len(resp.Summary.OutOfTune) == 0 {
		resp.Summary.Verdict = "pass"
	} else {
		resp.Summary.Verdict = "fail"
	}
	return resp
}

// parseField extracts exactly one finite, positive float from the form.
func parseField(form url.Values, name string) (float64, *fieldError) {
	values, ok := form[name]
	if !ok || len(values) == 0 {
		return 0, &fieldError{Field: name, Reason: "missing"}
	}
	if len(values) > 1 {
		return 0, &fieldError{Field: name, Reason: "duplicate"}
	}
	f, err := strconv.ParseFloat(values[0], 64)
	if err != nil {
		return 0, &fieldError{Field: name, Reason: "unparseable"}
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, &fieldError{Field: name, Reason: "not_finite"}
	}
	if f <= 0 {
		return 0, &fieldError{Field: name, Reason: "not_positive"}
	}
	return f, nil
}

// assessHandler handles POST /assess. Any invalid field aborts the whole
// assessment with 422 and no tuning conclusion is produced.
func assessHandler(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.JSON(http.StatusUnprocessableEntity, errorResponse{
			Error:  "invalid_form",
			Fields: []fieldError{},
		})
		return
	}
	measured := make(map[string]float64, len(partialSpecs))
	var errs []fieldError
	for _, spec := range partialSpecs {
		f, ferr := parseField(c.Request.PostForm, spec.name)
		if ferr != nil {
			errs = append(errs, *ferr)
			continue
		}
		if spec.name == "hum" && (f < humMinHz || f > humMaxHz) {
			errs = append(errs, fieldError{Field: spec.name, Reason: "out_of_range"})
			continue
		}
		measured[spec.name] = f
	}
	if len(errs) > 0 {
		c.JSON(http.StatusUnprocessableEntity, errorResponse{Error: "invalid_fields", Fields: errs})
		return
	}
	c.JSON(http.StatusOK, assess(measured))
}
