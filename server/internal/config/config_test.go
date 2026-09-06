package config

import (
	"testing"
	"time"
)

// Malformed env values fall back to the default instead of crashing the
// process at boot — and must not silently become 0/false.
func TestGetenvHelpersFallBackOnGarbage(t *testing.T) {
	t.Setenv("T_INT", "80GB")
	if got := getenvInt("T_INT", 80); got != 80 {
		t.Errorf("int garbage → %d", got)
	}
	t.Setenv("T_INT", "12")
	if got := getenvInt("T_INT", 80); got != 12 {
		t.Errorf("int → %d", got)
	}
	t.Setenv("T_BOOL", "yes-please")
	if got := getenvBool("T_BOOL", true); got != true {
		t.Errorf("bool garbage → %v", got)
	}
	t.Setenv("T_BOOL", "false")
	if got := getenvBool("T_BOOL", true); got != false {
		t.Errorf("bool → %v", got)
	}
	t.Setenv("T_DUR", "soon")
	if got := getenvDuration("T_DUR", 4*time.Minute); got != 4*time.Minute {
		t.Errorf("duration garbage → %v", got)
	}
	t.Setenv("T_DUR", "90s")
	if got := getenvDuration("T_DUR", 4*time.Minute); got != 90*time.Second {
		t.Errorf("duration → %v", got)
	}
}

func TestLoadHasSaneDefaults(t *testing.T) {
	c := Load()
	if c.HTTPAddr == "" || c.DataDir == "" {
		t.Fatalf("defaults missing: %+v", c)
	}
}
