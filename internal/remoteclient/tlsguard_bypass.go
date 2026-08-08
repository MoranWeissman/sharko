//go:build sharko_unverified_dest_ok

package remoteclient

// allowUnverifiedDestinations, TEST-BUILD state: on. Compiled only when
// the build passes `-tags sharko_unverified_dest_ok` — see
// tlsguard_default.go for why the production state cannot be switched on
// at runtime. With this tag, NewClientFromKubeconfig stops marking
// skip-verify clients and CheckDestinationTLS always returns nil, so a
// kind-based e2e harness or a demo estate can deliver to clusters that
// only speak self-signed TLS.
const allowUnverifiedDestinations = true
