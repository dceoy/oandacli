package cli

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dceoy/oandactl/oanda"
)

// planTTL is how long a plan hash printed by --dry-run stays valid.
const planTTL = 10 * time.Minute

// planHash binds a state-changing request to its environment, account,
// method, URL, body and expiry. The format is "<unix expiry>.<sha256 hex>".
func planHash(env oanda.Environment, account, method, url string, body []byte, expires int64) string {
	exp := strconv.FormatInt(expires, 10)
	h := sha256.New()
	for _, f := range []string{"oandactl-plan-v1", string(env), account, method, url, exp} {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	h.Write(body)
	return exp + "." + hex.EncodeToString(h.Sum(nil))
}

// planCheck returns an oanda.WithMutationCheck callback that allows a request
// only if it hashes to plan, and consumes the plan by atomically creating a
// marker file in dir so that it authorizes a single request across
// invocations. A consumed plan stays used even if sending then fails.
func planCheck(plan string, env oanda.Environment, account, dir string, now func() time.Time) func(string, string, []byte) error {
	return func(method, url string, body []byte) error {
		exp, _, _ := strings.Cut(plan, ".")
		expires, err := strconv.ParseInt(exp, 10, 64)
		t := now().Unix()
		switch {
		case err != nil:
			return &PolicyError{Msg: "--confirm-plan is malformed; copy planHash from the --dry-run output"}
		case t > expires:
			return &PolicyError{Msg: "--confirm-plan has expired; run --dry-run again"}
		case expires > t+int64(planTTL/time.Second):
			return &PolicyError{Msg: "--confirm-plan expiry is too far in the future"}
		case subtle.ConstantTimeCompare([]byte(planHash(env, account, method, url, body, expires)), []byte(plan)) != 1:
			return &PolicyError{Msg: "--confirm-plan does not match this request; nothing was sent"}
		}
		return consumePlan(dir, plan, t)
	}
}

// consumePlan records plan (already verified, so safe as a file name) as used
// and prunes markers of expired plans.
func consumePlan(dir, plan string, now int64) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &PolicyError{Msg: "cannot record --confirm-plan use: " + err.Error()}
	}
	f, err := os.OpenFile(filepath.Join(dir, plan), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- name is a verified plan hash
	if errors.Is(err, fs.ErrExist) {
		return &PolicyError{Msg: "--confirm-plan was already used; run --dry-run again"}
	}
	if err != nil {
		return &PolicyError{Msg: "cannot record --confirm-plan use: " + err.Error()}
	}
	_ = f.Close()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		exp, _, _ := strings.Cut(e.Name(), ".")
		if n, err := strconv.ParseInt(exp, 10, 64); err == nil && n < now {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}
