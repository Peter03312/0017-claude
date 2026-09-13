package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// shifted returns the Hz string that sits `cents` away from target.
func shifted(target, cents float64) string {
	return strconv.FormatFloat(target*math.Exp2(cents/1200), 'g', -1, 64)
}

type partialJSON struct {
	Name           string `json:"name"`
	TargetHz       string `json:"target_hz"`
	MeasuredHz     string `json:"measured_hz"`
	DeviationCents string `json:"deviation_cents"`
	ToleranceCents int    `json:"tolerance_cents"`
	Result         string `json:"result"`
}

type assessJSON struct {
	Partials []partialJSON `json:"partials"`
	Summary  struct {
		Verdict   string   `json:"verdict"`
		OutOfTune []string `json:"out_of_tune"`
	} `json:"summary"`
}

type errorJSON struct {
	Error  string `json:"error"`
	Fields []struct {
		Field  string `json:"field"`
		Reason string `json:"reason"`
	} `json:"fields"`
}

// postRaw posts a form body and returns status code and raw response bytes.
func postRaw(t *testing.T, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/assess", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func postAssess(t *testing.T, values url.Values) (int, assessJSON, []byte) {
	t.Helper()
	code, raw := postRaw(t, values.Encode())
	var resp assessJSON
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("200 response is not valid JSON: %v\n%s", err, raw)
		}
	}
	return code, resp, raw
}

func postError(t *testing.T, values url.Values) (int, errorJSON, []byte) {
	t.Helper()
	code, raw := postRaw(t, values.Encode())
	var resp errorJSON
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("error response is not valid JSON: %v\n%s", err, raw)
	}
	return code, resp, raw
}

// perfectForm returns a fully in-tune bell with hum = 100 Hz.
func perfectForm() url.Values {
	return url.Values{
		"hum":     {"100"},
		"prime":   {"200"},
		"tierce":  {"240"},
		"quint":   {"300"},
		"nominal": {"400"},
	}
}

func TestDeviationCents(t *testing.T) {
	if got := deviationCents(200, 200); got != 0 {
		t.Errorf("unison: got %v, want 0", got)
	}
	if got := deviationCents(400, 200); got != 1200 {
		t.Errorf("octave up: got %v, want 1200", got)
	}
	if got := deviationCents(100, 200); got != -1200 {
		t.Errorf("octave down: got %v, want -1200", got)
	}
}

func TestWithinToleranceBoundaries(t *testing.T) {
	cases := []struct {
		cents, tol float64
		want       bool
	}{
		{5, 5, true},  // hum positive boundary passes
		{-5, 5, true}, // hum negative boundary passes
		{8, 8, true},  // other partials positive boundary passes
		{-8, 8, true}, // other partials negative boundary passes
		{0, 5, true},  // dead centre
		{math.Nextafter(5, 6), 5, false},
		{math.Nextafter(-5, -6), 5, false},
		{math.Nextafter(8, 9), 8, false},
		{math.Nextafter(-8, -9), 8, false},
	}
	for _, c := range cases {
		if got := withinTolerance(c.cents, c.tol); got != c.want {
			t.Errorf("withinTolerance(%v, %v) = %v, want %v", c.cents, c.tol, got, c.want)
		}
	}
}

func TestFormatCents(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.00"},
		{math.Copysign(0, -1), "0.00"}, // negative zero shows as 0.00
		{-0.001, "0.00"},               // rounds to zero from below
		{0.0049, "0.00"},               // rounds to zero from above
		{0.125, "+0.13"},               // half rounds away from zero
		{-0.125, "-0.13"},
		{3.2, "+3.20"},
		{-3.2, "-3.20"},
		{8, "+8.00"},
		{4.996, "+5.00"}, // display rounds up to the tolerance boundary
		{5.004, "+5.00"},
		{-4.996, "-5.00"},
		{-5.004, "-5.00"},
	}
	for _, c := range cases {
		if got := formatCents(c.in); got != c.want {
			t.Errorf("formatCents(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatHz(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{100, "100.00"},
		{2.4 * 432.1, "1037.04"},
		{200.5, "200.50"},
		{99.999, "100.00"},
	}
	for _, c := range cases {
		if got := formatHz(c.in); got != c.want {
			t.Errorf("formatHz(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAllPartialsPass(t *testing.T) {
	code, resp, raw := postAssess(t, perfectForm())
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", code, raw)
	}
	wantNames := []string{"hum", "prime", "tierce", "quint", "nominal"}
	wantTargets := []string{"100.00", "200.00", "240.00", "300.00", "400.00"}
	wantTols := []int{5, 8, 8, 8, 8}
	if len(resp.Partials) != len(wantNames) {
		t.Fatalf("got %d partials, want %d", len(resp.Partials), len(wantNames))
	}
	for i, p := range resp.Partials {
		if p.Name != wantNames[i] {
			t.Errorf("partials[%d].name = %q, want %q (fixed order)", i, p.Name, wantNames[i])
		}
		if p.TargetHz != wantTargets[i] {
			t.Errorf("partials[%d].target_hz = %q, want %q", i, p.TargetHz, wantTargets[i])
		}
		if p.MeasuredHz != wantTargets[i] {
			t.Errorf("partials[%d].measured_hz = %q, want %q", i, p.MeasuredHz, wantTargets[i])
		}
		if p.DeviationCents != "0.00" {
			t.Errorf("partials[%d].deviation_cents = %q, want 0.00", i, p.DeviationCents)
		}
		if p.ToleranceCents != wantTols[i] {
			t.Errorf("partials[%d].tolerance_cents = %d, want %d", i, p.ToleranceCents, wantTols[i])
		}
		if p.Result != "pass" {
			t.Errorf("partials[%d].result = %q, want pass", i, p.Result)
		}
	}
	if resp.Summary.Verdict != "pass" {
		t.Errorf("summary.verdict = %q, want pass", resp.Summary.Verdict)
	}
	if len(resp.Summary.OutOfTune) != 0 {
		t.Errorf("summary.out_of_tune = %v, want empty", resp.Summary.OutOfTune)
	}
	if !strings.Contains(string(raw), `"out_of_tune":[]`) {
		t.Errorf("out_of_tune should serialize as [], body: %s", raw)
	}
}

func TestHumRangeBoundariesAreInclusive(t *testing.T) {
	for _, v := range []url.Values{
		{"hum": {"20"}, "prime": {"40"}, "tierce": {"48"}, "quint": {"60"}, "nominal": {"80"}},
		{"hum": {"1000"}, "prime": {"2000"}, "tierce": {"2400"}, "quint": {"3000"}, "nominal": {"4000"}},
	} {
		code, resp, raw := postAssess(t, v)
		if code != http.StatusOK || resp.Summary.Verdict != "pass" {
			t.Errorf("hum=%s: status=%d verdict=%q, want 200/pass; body: %s",
				v.Get("hum"), code, resp.Summary.Verdict, raw)
		}
	}
}

func TestOutOfTunePartialsListedInFixedOrder(t *testing.T) {
	v := perfectForm()
	v.Set("prime", shifted(200, 9))   // +9 cents, beyond ±8
	v.Set("quint", shifted(300, -20)) // -20 cents, beyond ±8
	code, resp, raw := postAssess(t, v)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", code, raw)
	}
	if resp.Summary.Verdict != "fail" {
		t.Errorf("summary.verdict = %q, want fail", resp.Summary.Verdict)
	}
	want := []string{"prime", "quint"}
	if len(resp.Summary.OutOfTune) != len(want) {
		t.Fatalf("out_of_tune = %v, want %v", resp.Summary.OutOfTune, want)
	}
	for i, name := range want {
		if resp.Summary.OutOfTune[i] != name {
			t.Errorf("out_of_tune[%d] = %q, want %q", i, resp.Summary.OutOfTune[i], name)
		}
	}
	if resp.Partials[0].Result != "pass" || resp.Partials[1].Result != "fail" ||
		resp.Partials[2].Result != "pass" || resp.Partials[3].Result != "fail" ||
		resp.Partials[4].Result != "pass" {
		t.Errorf("unexpected per-partial results: %+v", resp.Partials)
	}
	if resp.Partials[1].DeviationCents != "+9.00" {
		t.Errorf("prime deviation = %q, want +9.00", resp.Partials[1].DeviationCents)
	}
	if resp.Partials[3].DeviationCents != "-20.00" {
		t.Errorf("quint deviation = %q, want -20.00", resp.Partials[3].DeviationCents)
	}
}

// TestRoundingDoesNotChangeVerdict: verdicts come from unrounded cents, so two
// deviations that both display as "+8.00" (or "-8.00") can still differ in result.
func TestRoundingDoesNotChangeVerdict(t *testing.T) {
	cases := []struct {
		name       string
		cents      float64
		wantDev    string
		wantResult string
	}{
		{"just inside +8", 7.996, "+8.00", "pass"},
		{"just outside +8", 8.004, "+8.00", "fail"},
		{"just inside -8", -7.996, "-8.00", "pass"},
		{"just outside -8", -8.004, "-8.00", "fail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := perfectForm()
			v.Set("prime", shifted(200, c.cents))
			code, resp, raw := postAssess(t, v)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", code, raw)
			}
			prime := resp.Partials[1]
			if prime.DeviationCents != c.wantDev {
				t.Errorf("deviation_cents = %q, want %q", prime.DeviationCents, c.wantDev)
			}
			if prime.Result != c.wantResult {
				t.Errorf("result = %q, want %q (display %q must not change the verdict)",
					prime.Result, c.wantResult, prime.DeviationCents)
			}
			wantVerdict := "pass"
			if c.wantResult == "fail" {
				wantVerdict = "fail"
			}
			if resp.Summary.Verdict != wantVerdict {
				t.Errorf("summary.verdict = %q, want %q", resp.Summary.Verdict, wantVerdict)
			}
		})
	}
}

func TestNegativeZeroDeviationDisplaysAsZero(t *testing.T) {
	v := perfectForm()
	v.Set("tierce", shifted(240, -0.001)) // slightly flat, rounds to -0.00
	code, resp, raw := postAssess(t, v)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", code, raw)
	}
	if got := resp.Partials[2].DeviationCents; got != "0.00" {
		t.Errorf("tierce deviation = %q, want 0.00 (no negative zero)", got)
	}
	if resp.Partials[2].Result != "pass" {
		t.Errorf("tierce result = %q, want pass", resp.Partials[2].Result)
	}
}

func TestInvalidFloatValues(t *testing.T) {
	cases := []struct {
		field  string
		value  string
		reason string
	}{
		{"hum", "abc", "unparseable"},
		{"hum", "", "unparseable"},
		{"hum", "10c", "unparseable"},
		{"hum", "1e999", "unparseable"}, // overflows float64
		{"hum", "NaN", "not_finite"},
		{"hum", "nan", "not_finite"},
		{"hum", "Inf", "not_finite"},
		{"hum", "+Inf", "not_finite"},
		{"hum", "-Inf", "not_finite"},
		{"hum", "Infinity", "not_finite"},
		{"hum", "-1", "not_positive"},
		{"hum", "0", "not_positive"},
		{"hum", "-0.0", "not_positive"},
		{"hum", "19.999", "out_of_range"},
		{"hum", "1000.001", "out_of_range"},
		{"prime", "xyz", "unparseable"},
		{"prime", "NaN", "not_finite"},
		{"prime", "-Inf", "not_finite"},
		{"prime", "-5", "not_positive"},
		{"tierce", "0", "not_positive"},
		{"quint", "1e999", "unparseable"},
		{"nominal", "NaN", "not_finite"},
	}
	for _, c := range cases {
		t.Run(c.field+"="+c.value, func(t *testing.T) {
			v := perfectForm()
			v.Set(c.field, c.value)
			code, resp, raw := postError(t, v)
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body: %s", code, raw)
			}
			if resp.Error != "invalid_fields" {
				t.Errorf("error = %q, want invalid_fields", resp.Error)
			}
			if len(resp.Fields) != 1 {
				t.Fatalf("got %d field errors, want 1: %v", len(resp.Fields), resp.Fields)
			}
			if resp.Fields[0].Field != c.field || resp.Fields[0].Reason != c.reason {
				t.Errorf("field error = %+v, want {%s %s}", resp.Fields[0], c.field, c.reason)
			}
			if strings.Contains(string(raw), "partials") {
				t.Errorf("422 response must not contain a tuning conclusion, body: %s", raw)
			}
		})
	}
}

func TestMissingAndDuplicateFields(t *testing.T) {
	t.Run("missing one field", func(t *testing.T) {
		v := perfectForm()
		v.Del("tierce")
		code, resp, _ := postError(t, v)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", code)
		}
		if len(resp.Fields) != 1 || resp.Fields[0].Field != "tierce" || resp.Fields[0].Reason != "missing" {
			t.Errorf("fields = %+v, want [{tierce missing}]", resp.Fields)
		}
	})

	t.Run("duplicate field", func(t *testing.T) {
		code, resp, _ := postError(t, url.Values{
			"hum":     {"100", "200"}, // duplicated
			"prime":   {"200"},
			"tierce":  {"240"},
			"quint":   {"300"},
			"nominal": {"400"},
		})
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", code)
		}
		if len(resp.Fields) != 1 || resp.Fields[0].Field != "hum" || resp.Fields[0].Reason != "duplicate" {
			t.Errorf("fields = %+v, want [{hum duplicate}]", resp.Fields)
		}
	})

	t.Run("empty body reports all fields missing in fixed order", func(t *testing.T) {
		code, raw := postRaw(t, "")
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", code)
		}
		var resp errorJSON
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		want := []string{"hum", "prime", "tierce", "quint", "nominal"}
		if len(resp.Fields) != len(want) {
			t.Fatalf("got %d field errors, want %d", len(resp.Fields), len(want))
		}
		for i, name := range want {
			if resp.Fields[i].Field != name || resp.Fields[i].Reason != "missing" {
				t.Errorf("fields[%d] = %+v, want {%s missing}", i, resp.Fields[i], name)
			}
		}
	})
}

func TestMultipleInvalidFieldsAllReported(t *testing.T) {
	v := perfectForm()
	v.Set("hum", "abc")
	v.Set("quint", "NaN")
	v.Del("nominal")
	code, resp, _ := postError(t, v)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	want := []struct{ field, reason string }{
		{"hum", "unparseable"},
		{"quint", "not_finite"},
		{"nominal", "missing"},
	}
	if len(resp.Fields) != len(want) {
		t.Fatalf("fields = %+v, want %d entries", resp.Fields, len(want))
	}
	for i, w := range want {
		if resp.Fields[i].Field != w.field || resp.Fields[i].Reason != w.reason {
			t.Errorf("fields[%d] = %+v, want {%s %s}", i, resp.Fields[i], w.field, w.reason)
		}
	}
}

func TestNonHumFieldsHaveNoUpperBound(t *testing.T) {
	v := perfectForm()
	v.Set("prime", "1000000") // wildly sharp but a valid finite positive number
	code, resp, raw := postAssess(t, v)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", code, raw)
	}
	if resp.Partials[1].Result != "fail" || resp.Summary.Verdict != "fail" {
		t.Errorf("prime=1e6 should be assessed and fail, got %+v / %q", resp.Partials[1], resp.Summary.Verdict)
	}
}
