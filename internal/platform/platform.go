// Package platform resolves runtime facts seedtensor needs: which inference
// backend to serve, and how (or whether) the inference process can be denied
// network access on this OS.
package platform

import (
	"runtime"

	"github.com/Alexintosh/SeedTensor/internal/config"
)

// Backend is the resolved inference backend.
type Backend string

const (
	BackendMetal Backend = "metal"
	BackendCUDA  Backend = "cuda"
)

// Resolve maps a configured platform to a concrete backend, detecting when set
// to auto. Detection is intentionally conservative: Apple → Metal, otherwise
// CUDA, with the caller free to override via config.
func Resolve(p config.Platform) (Backend, error) {
	switch p {
	case config.PlatformMetal:
		return BackendMetal, nil
	case config.PlatformCUDA:
		return BackendCUDA, nil
	case config.PlatformAuto, "":
		if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			return BackendMetal, nil
		}
		return BackendCUDA, nil
	default:
		return "", &UnsupportedError{Platform: string(p)}
	}
}

// UnsupportedError reports an unknown platform value.
type UnsupportedError struct{ Platform string }

func (e *UnsupportedError) Error() string { return "unsupported platform: " + e.Platform }

// NetworkIsolation describes whether the inference process can be run with no
// network access on this host, and how.
type NetworkIsolation struct {
	// Enforceable is true when the OS gives us a way to deny the child
	// process network access that seedtensor can apply itself.
	Enforceable bool
	// Mechanism names the method (e.g. "linux-netns", "none").
	Mechanism string
	// Detail is a human explanation, surfaced to the operator so the
	// security guarantee is never silently weaker than claimed.
	Detail string
}

// DetectNetworkIsolation reports how the inference child can be network-denied.
//
// On Linux we can run the child in a new, empty network namespace, which
// removes every interface except loopback: the process physically cannot reach
// the network. On macOS there is no unprivileged per-process network namespace,
// so seedtensor cannot enforce this itself; the honest answer is that the operator
// must rely on an outer sandbox (or accept the gap), and we say so rather than
// pretend.
func DetectNetworkIsolation() NetworkIsolation {
	switch runtime.GOOS {
	case "linux":
		return NetworkIsolation{
			Enforceable: true,
			Mechanism:   "linux-netns",
			Detail:      "inference runs in a new network namespace with only loopback",
		}
	case "darwin":
		return NetworkIsolation{
			Enforceable: false,
			Mechanism:   "none",
			Detail:      "macOS has no unprivileged per-process network namespace; seedtensor cannot enforce no-network on the inference process itself. Run seedtensor under an outer sandbox, or accept that TensorFold could in principle open a socket.",
		}
	default:
		return NetworkIsolation{
			Enforceable: false,
			Mechanism:   "none",
			Detail:      "no supported network-isolation mechanism on " + runtime.GOOS,
		}
	}
}
