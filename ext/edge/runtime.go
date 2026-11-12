package edge

import (
	"net/http"
)

// Runtime runs instances on this edge: ext/pocker in-process, ext/docker in containers.
type Runtime interface {
	// Name identifies the runtime ("pocker", "docker"); an instance may ask for one in [Instance.Runtime].
	Name() string
	// Serves reports whether this runtime can run inst (e.g. its PocketBase version).
	Serves(inst Instance) bool
	// ServeInstance serves a request for inst, starting it on demand. The host has already resolved the instance,
	// admitted the request (gates) and decided it runs here.
	ServeInstance(w http.ResponseWriter, r *http.Request, inst Instance)
	// Ready reports whether the runtime can take requests.
	Ready() bool
}

// Prechecker is an optional runtime hook that runs before the gates admit a request for a local instance, e.g. to
// answer 503 for an instance under maintenance or paused, without counting it against rate limits. false = answered.
type Prechecker interface {
	Precheck(w http.ResponseWriter, r *http.Request, inst Instance) bool
}

// Syncer is an optional runtime hook for directory changes (stop removed instances, restart changed ones).
type Syncer interface {
	Sync(c Change, prev *Instance)
}

// Shutdowner is an optional runtime hook called when the host stops.
type Shutdowner interface {
	Shutdown()
}

// Statser is an optional runtime hook for the health route: how many instances it has running (RunningCount).
type Statser interface {
	RunningCount() int
}

// pick chooses the runtime for inst: the one it names in Runtime, else the first that serves it.
func pick(runtimes []Runtime, inst Instance) Runtime {
	if inst.Runtime != "" {
		for _, rt := range runtimes {
			if rt.Name() == inst.Runtime {
				return rt
			}
		}
		return nil
	}
	for _, rt := range runtimes {
		if rt.Serves(inst) {
			return rt
		}
	}
	return nil
}
