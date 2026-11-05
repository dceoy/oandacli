package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dceoy/oandactl/oanda"
)

func TestReadOnlyPreviews(t *testing.T) {
	for _, command := range [][]string{
		{"order", "market", "--instrument", "EUR_USD", "--units", "100"},
		{"trade", "close", "--trade", "123"},
	} {
		for _, tt := range []struct {
			name string
			env  map[string]string
			args []string
			code int
		}{
			{"env preview", map[string]string{EnvReadOnly: "true"}, []string{"--dry-run"}, ExitOK},
			{"flag preview", nil, []string{"--read-only", "--dry-run"}, ExitOK},
			{"live required preview", map[string]string{EnvReadOnly: "true", EnvEnvironment: "live", EnvRequirePlan: "true"}, []string{"--dry-run"}, ExitOK},
			{"env execution", map[string]string{EnvReadOnly: "true"}, nil, ExitPolicy},
			{"flag execution", nil, []string{"--read-only"}, ExitPolicy},
			{"false flags cannot revoke env", map[string]string{EnvReadOnly: "true"}, []string{"--read-only=false", "--dry-run=false"}, ExitPolicy},
		} {
			t.Run(command[0]+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, okJSON(`{}`))
				for k, v := range tt.env {
					h.env[k] = v
				}
				args := append(append([]string{}, command...), tt.args...)
				if code := h.run(args...); code != tt.code {
					t.Fatalf("exit %d, want %d: %s", code, tt.code, h.stderr.String())
				}
				if h.calls.Load() != 0 {
					t.Fatal("read-only invocation reached server")
				}
				if tt.code == ExitOK {
					var plan struct {
						DryRun   bool   `json:"dryRun"`
						PlanHash string `json:"planHash"`
					}
					if err := json.Unmarshal(h.stdout.Bytes(), &plan); err != nil || !plan.DryRun || plan.PlanHash == "" {
						t.Fatalf("invalid preview: %s", h.stdout.String())
					}
				}
			})
		}
	}
}

func TestPlanTTL(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  string
		flag string
		want time.Duration
	}{
		{"default", "", "", 10 * time.Minute},
		{"env", "30m", "", 30 * time.Minute},
		{"flag wins", "30m", "15m", 15 * time.Minute},
		{"flag overrides invalid env", "invalid", "10m", 10 * time.Minute},
		{"minimum", "", "1m", time.Minute},
		{"maximum", "", "60m", time.Hour},
		{"below minimum", "", "59s", 0},
		{"above maximum", "", "60m1s", 0},
		{"zero", "", "0", 0},
		{"negative", "-1m", "", 0},
		{"invalid env", "invalid", "", 0},
		{"invalid flag", "", "invalid", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, okJSON(`{}`))
			h.env[EnvPlanTTL] = tt.env
			args := []string{"order", "market", "--instrument", "EUR_USD", "--units", "100", "--dry-run"}
			if tt.flag != "" {
				args = append(args, "--plan-ttl", tt.flag)
			}
			before := time.Now()
			code := h.run(args...)
			if h.calls.Load() != 0 {
				t.Fatal("preview reached server")
			}
			if tt.want == 0 {
				if code != ExitUsage {
					t.Fatalf("exit %d, want usage: %s", code, h.stderr.String())
				}
				return
			}
			if code != ExitOK {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			var plan struct {
				Request       oanda.DryRunError `json:"request"`
				PlanHash      string            `json:"planHash"`
				PlanExpiresAt time.Time         `json:"planExpiresAt"`
			}
			if err := json.Unmarshal(h.stdout.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if exp := plan.PlanExpiresAt.Unix(); exp < before.Add(tt.want).Unix() || exp > time.Now().Add(tt.want).Unix() {
				t.Fatalf("expiry %v does not reflect TTL %v", plan.PlanExpiresAt, tt.want)
			}
			wantHash := planHash(oanda.Practice, testAccount, plan.Request.Method, plan.Request.URL, plan.Request.Body, plan.PlanExpiresAt.Unix())
			if plan.PlanHash != wantHash {
				t.Fatal("hash does not bind advertised expiry")
			}
			// Execution need not repeat the preview's TTL configuration.
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			delete(h.env, EnvPlanTTL)
			if code := h.run("order", "market", "--instrument", "EUR_USD", "--units", "100", "--confirm-plan", plan.PlanHash); code != ExitOK {
				t.Fatalf("confirmation exit %d: %s", code, h.stderr.String())
			}
			if h.calls.Load() != 1 {
				t.Fatalf("server calls = %d", h.calls.Load())
			}
		})
	}
}

func TestPlanExpiryBound(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tt := range []struct {
		name string
		ttl  time.Duration
		want bool
	}{
		{"expired", -time.Second, false},
		{"default", planTTL, true},
		{"upper bound", maxPlanTTL, true},
		{"over bound", maxPlanTTL + time.Second, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := planHash(oanda.Practice, testAccount, "PUT", "https://x/v3", nil, now.Add(tt.ttl).Unix())
			err := planCheck(plan, oanda.Practice, testAccount, filepath.Join(t.TempDir(), "plans"), func() time.Time { return now })("PUT", "https://x/v3", nil)
			var policy *PolicyError
			if (err == nil) != tt.want || (err != nil && !errors.As(err, &policy)) {
				t.Fatalf("check = %v, want allowed %v", err, tt.want)
			}
		})
	}
}
