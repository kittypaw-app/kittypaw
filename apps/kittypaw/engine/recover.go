package engine

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// RecoverAccountPanic is the single panic-recovery chokepoint for account
// goroutines — one account's panic must not take down the shared server
// (account-level panic isolation). site names the caller
// ("scheduler.runSkill", "server.dispatchLoop") so structured logs
// identify which layer caught the panic. Nil-safe on sess and
// sess.Health for bare-struct test fixtures.
func RecoverAccountPanic(sess *AccountRuntime, site string, r any) {
	account := ""
	if sess != nil {
		account = sess.AccountID
	}
	slog.Error("account_panic_recovered",
		"account", account,
		"site", site,
		"panic", fmt.Sprintf("%v", r),
		"stack", string(debug.Stack()),
	)
	if sess != nil && sess.Health != nil {
		sess.Health.MarkDegraded(time.Now())
	}
}

// MarkAccountReady promotes Health back to Ready on clean completion so a
// transient panic self-heals on the next successful iteration. Nil-safe.
func MarkAccountReady(sess *AccountRuntime) {
	if sess == nil || sess.Health == nil {
		return
	}
	sess.Health.MarkReady()
}

// runWithAccountRecover executes fn under a deferred recover. A panic
// marks the account Degraded via RecoverAccountPanic; clean completion
// promotes it back to Ready. Use from every worker goroutine where a
// single panic should not wedge the account.
func runWithAccountRecover(sess *AccountRuntime, site string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			RecoverAccountPanic(sess, site, r)
			return
		}
		MarkAccountReady(sess)
	}()
	fn()
}
