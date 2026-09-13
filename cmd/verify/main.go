// Command verify is a one-shot acceptance client: it runs black-box checks
// against a running belltune API and exits non-zero if any check fails.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

var apiBase = "http://api:8080"

type partial struct {
	Name           string `json:"name"`
	TargetHz       string `json:"target_hz"`
	MeasuredHz     string `json:"measured_hz"`
	DeviationCents string `json:"deviation_cents"`
	ToleranceCents int    `json:"tolerance_cents"`
	Result         string `json:"result"`
}

type assessResponse struct {
	Partials []partial `json:"partials"`
	Summary  struct {
		Verdict   string   `json:"verdict"`
		OutOfTune []string `json:"out_of_tune"`
	} `json:"summary"`
}

type errorResponse struct {
	Error  string `json:"error"`
	Fields []struct {
		Field  string `json:"field"`
		Reason string `json:"reason"`
	} `json:"fields"`
}

// shifted returns the Hz string that sits `cents` away from target.
func shifted(target, cents float64) string {
	return strconv.FormatFloat(target*math.Exp2(cents/1200), 'g', -1, 64)
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

func postRaw(body string) (int, []byte, error) {
	resp, err := http.Post(apiBase+"/assess", "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func postAssess(v url.Values) (assessResponse, []byte, error) {
	var ar assessResponse
	code, raw, err := postRaw(v.Encode())
	if err != nil {
		return ar, raw, err
	}
	if code != http.StatusOK {
		return ar, raw, fmt.Errorf("status = %d, want 200; body: %s", code, raw)
	}
	if err := json.Unmarshal(raw, &ar); err != nil {
		return ar, raw, fmt.Errorf("invalid JSON: %v", err)
	}
	return ar, raw, nil
}

func postError(v url.Values) (errorResponse, []byte, error) {
	var er errorResponse
	code, raw, err := postRaw(v.Encode())
	if err != nil {
		return er, raw, err
	}
	if code != http.StatusUnprocessableEntity {
		return er, raw, fmt.Errorf("status = %d, want 422; body: %s", code, raw)
	}
	if err := json.Unmarshal(raw, &er); err != nil {
		return er, raw, fmt.Errorf("invalid JSON: %v", err)
	}
	if strings.Contains(string(raw), "partials") {
		return er, raw, fmt.Errorf("422 response must not contain a tuning conclusion: %s", raw)
	}
	return er, raw, nil
}

func waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(apiBase + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s/healthz", apiBase)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func checkPerfectBell() error {
	r, _, err := postAssess(perfectForm())
	if err != nil {
		return err
	}
	wantNames := []string{"hum", "prime", "tierce", "quint", "nominal"}
	wantTargets := []string{"100.00", "200.00", "240.00", "300.00", "400.00"}
	wantTols := []int{5, 8, 8, 8, 8}
	if len(r.Partials) != len(wantNames) {
		return fmt.Errorf("got %d partials, want %d", len(r.Partials), len(wantNames))
	}
	for i, p := range r.Partials {
		switch {
		case p.Name != wantNames[i]:
			return fmt.Errorf("partials[%d].name = %q, want %q", i, p.Name, wantNames[i])
		case p.TargetHz != wantTargets[i]:
			return fmt.Errorf("partials[%d].target_hz = %q, want %q", i, p.TargetHz, wantTargets[i])
		case p.MeasuredHz != wantTargets[i]:
			return fmt.Errorf("partials[%d].measured_hz = %q, want %q", i, p.MeasuredHz, wantTargets[i])
		case p.DeviationCents != "0.00":
			return fmt.Errorf("partials[%d].deviation_cents = %q, want 0.00", i, p.DeviationCents)
		case p.ToleranceCents != wantTols[i]:
			return fmt.Errorf("partials[%d].tolerance_cents = %d, want %d", i, p.ToleranceCents, wantTols[i])
		case p.Result != "pass":
			return fmt.Errorf("partials[%d].result = %q, want pass", i, p.Result)
		}
	}
	if r.Summary.Verdict != "pass" {
		return fmt.Errorf("summary.verdict = %q, want pass", r.Summary.Verdict)
	}
	if len(r.Summary.OutOfTune) != 0 {
		return fmt.Errorf("summary.out_of_tune = %v, want empty", r.Summary.OutOfTune)
	}
	return nil
}

func checkOutOfTuneListed() error {
	v := perfectForm()
	v.Set("prime", shifted(200, 30))
	v.Set("quint", shifted(300, -50))
	r, _, err := postAssess(v)
	if err != nil {
		return err
	}
	if r.Summary.Verdict != "fail" {
		return fmt.Errorf("summary.verdict = %q, want fail", r.Summary.Verdict)
	}
	if !slices.Equal(r.Summary.OutOfTune, []string{"prime", "quint"}) {
		return fmt.Errorf("out_of_tune = %v, want [prime quint]", r.Summary.OutOfTune)
	}
	if r.Partials[1].DeviationCents != "+30.00" || r.Partials[3].DeviationCents != "-50.00" {
		return fmt.Errorf("deviations = %q / %q, want +30.00 / -50.00",
			r.Partials[1].DeviationCents, r.Partials[3].DeviationCents)
	}
	return nil
}

func checkDeviationFormatting() error {
	v := perfectForm()
	v.Set("prime", shifted(200, 3.21))
	v.Set("tierce", shifted(240, -1.5))
	r, _, err := postAssess(v)
	if err != nil {
		return err
	}
	if r.Partials[1].DeviationCents != "+3.21" {
		return fmt.Errorf("prime deviation = %q, want +3.21", r.Partials[1].DeviationCents)
	}
	if r.Partials[2].DeviationCents != "-1.50" {
		return fmt.Errorf("tierce deviation = %q, want -1.50", r.Partials[2].DeviationCents)
	}
	if r.Summary.Verdict != "pass" {
		return fmt.Errorf("summary.verdict = %q, want pass", r.Summary.Verdict)
	}
	return nil
}

// checkRoundingDoesNotChangeVerdict: deviations of ±7.9999 and ±8.0001 cents
// all display as ±8.00, but only the ones inside the tolerance may pass.
func checkRoundingDoesNotChangeVerdict() error {
	cases := []struct {
		cents      float64
		wantDev    string
		wantResult string
	}{
		{7.9999, "+8.00", "pass"},
		{8.0001, "+8.00", "fail"},
		{-7.9999, "-8.00", "pass"},
		{-8.0001, "-8.00", "fail"},
	}
	for _, c := range cases {
		v := perfectForm()
		v.Set("prime", shifted(200, c.cents))
		r, _, err := postAssess(v)
		if err != nil {
			return err
		}
		if r.Partials[1].DeviationCents != c.wantDev {
			return fmt.Errorf("cents=%v: deviation = %q, want %q", c.cents, r.Partials[1].DeviationCents, c.wantDev)
		}
		if r.Partials[1].Result != c.wantResult {
			return fmt.Errorf("cents=%v: result = %q, want %q (display %q must not decide)",
				c.cents, r.Partials[1].Result, c.wantResult, c.wantDev)
		}
	}
	return nil
}

func checkMissingField() error {
	v := perfectForm()
	v.Del("tierce")
	er, _, err := postError(v)
	if err != nil {
		return err
	}
	if er.Error != "invalid_fields" || len(er.Fields) != 1 ||
		er.Fields[0].Field != "tierce" || er.Fields[0].Reason != "missing" {
		return fmt.Errorf("got %+v, want one {tierce missing} error", er)
	}
	return nil
}

func checkDuplicateField() error {
	code, raw, err := postRaw("hum=100&hum=200&prime=200&tierce=240&quint=300&nominal=400")
	if err != nil {
		return err
	}
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("status = %d, want 422; body: %s", code, raw)
	}
	var er errorResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	if len(er.Fields) != 1 || er.Fields[0].Field != "hum" || er.Fields[0].Reason != "duplicate" {
		return fmt.Errorf("got %+v, want one {hum duplicate} error", er)
	}
	return nil
}

func checkInvalidFloats() error {
	cases := []struct {
		field  string
		value  string
		reason string
	}{
		{"hum", "abc", "unparseable"},
		{"hum", "NaN", "not_finite"},
		{"prime", "+Inf", "not_finite"},
		{"prime", "-3", "not_positive"},
		{"quint", "0", "not_positive"},
		{"nominal", "-Inf", "not_finite"},
	}
	for _, c := range cases {
		v := perfectForm()
		v.Set(c.field, c.value)
		er, _, err := postError(v)
		if err != nil {
			return fmt.Errorf("%s=%s: %v", c.field, c.value, err)
		}
		if len(er.Fields) != 1 || er.Fields[0].Field != c.field || er.Fields[0].Reason != c.reason {
			return fmt.Errorf("%s=%s: got %+v, want one {%s %s} error",
				c.field, c.value, er.Fields, c.field, c.reason)
		}
	}
	return nil
}

func checkHumRange() error {
	for _, hum := range []string{"19.999", "1000.001"} {
		v := perfectForm()
		v.Set("hum", hum)
		er, _, err := postError(v)
		if err != nil {
			return fmt.Errorf("hum=%s: %v", hum, err)
		}
		if len(er.Fields) != 1 || er.Fields[0].Field != "hum" || er.Fields[0].Reason != "out_of_range" {
			return fmt.Errorf("hum=%s: got %+v, want one {hum out_of_range} error", hum, er.Fields)
		}
	}
	// Range boundaries are inclusive and must be accepted.
	for _, v := range []url.Values{
		{"hum": {"20"}, "prime": {"40"}, "tierce": {"48"}, "quint": {"60"}, "nominal": {"80"}},
		{"hum": {"1000"}, "prime": {"2000"}, "tierce": {"2400"}, "quint": {"3000"}, "nominal": {"4000"}},
	} {
		r, _, err := postAssess(v)
		if err != nil {
			return fmt.Errorf("hum=%s: %v", v.Get("hum"), err)
		}
		if r.Summary.Verdict != "pass" {
			return fmt.Errorf("hum=%s: verdict = %q, want pass", v.Get("hum"), r.Summary.Verdict)
		}
	}
	return nil
}

func main() {
	if v := os.Getenv("API_URL"); v != "" {
		apiBase = strings.TrimRight(v, "/")
	}
	if err := waitReady(60 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		os.Exit(1)
	}
	checks := []struct {
		name string
		fn   func() error
	}{
		{"perfect bell passes with fixed partial order", checkPerfectBell},
		{"out-of-tune partials listed for next scraping round", checkOutOfTuneListed},
		{"signed deviation formatting", checkDeviationFormatting},
		{"rounding to 2 decimals does not change the verdict", checkRoundingDoesNotChangeVerdict},
		{"missing field rejected with 422", checkMissingField},
		{"duplicate field rejected with 422", checkDuplicateField},
		{"invalid floats rejected with 422", checkInvalidFloats},
		{"hum range 20..1000 enforced, boundaries inclusive", checkHumRange},
	}
	failed := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			failed++
		} else {
			fmt.Printf("PASS %s\n", c.name)
		}
	}
	if failed > 0 {
		fmt.Printf("verify: %d of %d checks failed\n", failed, len(checks))
		os.Exit(1)
	}
	fmt.Printf("verify: all %d checks passed\n", len(checks))
}
