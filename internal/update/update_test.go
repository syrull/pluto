package update

import (
	"fmt"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.0.1", "v0.0.2", true},
		{"0.0.1", "0.0.2", true},
		{"v0.0.2", "v0.0.1", false},
		{"v0.0.1", "v0.0.1", false},
		{"v0.1.0", "v0.0.9", false},
		{"v0.9.9", "v1.0.0", true},
		{"dev", "v0.0.1", true},
		{"garbage", "v0.0.1", true},
		{"v0.0.1", "garbage", false},
		{"v1.2.3-rc1", "v1.2.3", false},
	}
	for _, tt := range tests {
		if got := newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	if v, ok := parse("v1.2.3"); !ok || v != [3]int{1, 2, 3} {
		t.Errorf("parse(v1.2.3) = %v, %v", v, ok)
	}
	if _, ok := parse("1.2"); ok {
		t.Error("parse(1.2) should fail")
	}
	if _, ok := parse("dev"); ok {
		t.Error("parse(dev) should fail")
	}
}

func TestAssetName(t *testing.T) {
	want := fmt.Sprintf("pluto_%s_%s", runtime.GOOS, runtime.GOARCH)
	if got := assetName(); got != want {
		t.Errorf("assetName() = %q, want %q", got, want)
	}
}
