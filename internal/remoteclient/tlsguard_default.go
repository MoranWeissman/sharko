//go:build !sharko_unverified_dest_ok

package remoteclient

// allowUnverifiedDestinations is the test-only bypass for the unverified-
// destination refusal (tlsguard.go), in its PRODUCTION state: off.
//
// This is a compile-time constant behind a build tag, on purpose. A normal
// build (`go build ./...`, the release Dockerfile, anything that does not
// pass `-tags sharko_unverified_dest_ok`) compiles this file, the constant
// is false, and nothing can change that at runtime — there is no
// environment variable, no config field, no API, no flag. The compiler can
// prove the bypass branch dead. The ONLY way to switch the bypass on is to
// compile a different binary with `-tags sharko_unverified_dest_ok`
// (tlsguard_bypass.go), which is what a kind-based e2e harness or a demo
// estate that genuinely needs to deliver over self-signed-cert clusters
// would do — and that binary is, visibly and by construction, not a
// production build.
const allowUnverifiedDestinations = false
