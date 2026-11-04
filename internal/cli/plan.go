package cli

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// planCheck returns an oanda.WithMutationCheck callback that allows exactly
// one request, and only if it hashes to plan.
func planCheck(plan string, env oanda.Environment, account string, now func() time.Time) func(string, string, []byte) error {
	used := false
	return func(method, url string, body []byte) error {
		exp, _, _ := strings.Cut(plan, ".")
		expires, err := strconv.ParseInt(exp, 10, 64)
		t := now().Unix()
		switch {
		case used:
			return &PolicyError{Msg: "--confirm-plan authorizes a single request"}
		case err != nil:
			return &PolicyError{Msg: "--confirm-plan is malformed; copy planHash from the --dry-run output"}
		case t > expires:
			return &PolicyError{Msg: "--confirm-plan has expired; run --dry-run again"}
		case expires > t+int64(planTTL/time.Second):
			return &PolicyError{Msg: "--confirm-plan expiry is too far in the future"}
		case subtle.ConstantTimeCompare([]byte(planHash(env, account, method, url, body, expires)), []byte(plan)) != 1:
			return &PolicyError{Msg: "--confirm-plan does not match this request; nothing was sent"}
		}
		used = true
		return nil
	}
}
