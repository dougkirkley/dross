package server

import (
	"crypto/rand"
	"math/big"
)

// Session workloads are named after the server that asked for them plus a
// friendly suffix — "dross-web-quiet-harbor" — rather than after the session
// id, which is a hex string nobody can hold in their head while reading
// `kubectl get pods`.
//
// The pair is drawn from these two lists, which is a little over nine hundred
// combinations. That is not enough to assume uniqueness, so Create retries on a
// name that already exists rather than pretending collisions cannot happen.
var (
	sessionAdjectives = []string{
		"amber", "brisk", "calm", "clever", "copper", "curious", "dusky",
		"eager", "fabled", "gentle", "hidden", "idle", "jolly", "keen",
		"lucid", "mellow", "nimble", "olive", "patient", "quiet", "rustic",
		"silent", "tidal", "umber", "vivid", "wandering", "yonder", "zealous",
	}
	sessionNouns = []string{
		"anchor", "beacon", "cedar", "delta", "ember", "fjord", "grove",
		"harbor", "island", "juniper", "kestrel", "lantern", "meadow", "nebula",
		"orchard", "prairie", "quarry", "ridge", "summit", "thicket", "vale",
		"willow", "yarrow", "zephyr",
	}
)

// friendlyName is the "<adjective>-<noun>" half, which is also what a session
// is called in the UI. The server prefix is left off there: every session on a
// server carries the same one, so repeating it down a list says nothing.
//
// crypto/rand rather than math/rand: not because the name is a secret, but
// because it needs no seeding to be different across the server's replicas and
// restarts, which is exactly the case a badly seeded generator gets wrong.
func friendlyName() string {
	return pick(sessionAdjectives) + "-" + pick(sessionNouns)
}

// sessionName returns "<prefix>-<friendly>", the workload's object name.
func sessionName(prefix, friendly string) string {
	if prefix == "" {
		prefix = "dross"
	}
	return prefix + "-" + friendly
}

func pick(from []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(from))))
	if err != nil {
		// A failing system RNG is not a reason to refuse a session; the retry
		// on AlreadyExists covers the repetition this can cause.
		return from[0]
	}
	return from[n.Int64()]
}
