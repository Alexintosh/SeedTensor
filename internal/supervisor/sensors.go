package supervisor

import (
	"os"
	"runtime"
	"strings"

	"github.com/Alexintosh/SeedTensor/internal/seed"
)

// hostSensors returns the host condition sensors for this OS.
//
// Battery detection is implemented for Linux via the sysfs power_supply
// interface. Metered-connection detection has no portable source, so it
// defaults to false (not metered); a user on a metered link should instead set
// seed.network appropriately or lower the daily cap. These defaults are
// conservative in the sense that they never *stop* seeding on a false signal;
// the honest gap (metered detection) is documented rather than faked.
func hostSensors() seed.Sensors {
	return seed.Sensors{
		BatteryFunc: onBattery,
		MeteredFunc: func() bool { return false },
	}
}

// onBattery reports whether the machine is running on battery.
func onBattery() bool {
	if runtime.GOOS != "linux" {
		// macOS battery state would need IOKit via cgo; not wired in Phase 1.
		// Returning false means we don't wrongly pause on a desktop Mac.
		return false
	}
	// On Linux, /sys/class/power_supply/AC*/online == 0 means on battery,
	// when an AC adapter entry exists.
	base := "/sys/class/power_supply"
	entries, err := os.ReadDir(base)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "AC") && !strings.HasPrefix(name, "ADP") {
			continue
		}
		raw, err := os.ReadFile(base + "/" + name + "/online")
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(raw)) == "0" {
			return true
		}
		return false
	}
	return false
}
