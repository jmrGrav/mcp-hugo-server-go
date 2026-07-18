package buildinfo

import "testing"

func TestEffectiveReleaseVersionAndBuildChannel(t *testing.T) {
	origVersion := Version
	origRelease := ReleaseVersion
	origChannel := BuildChannel
	defer func() {
		Version = origVersion
		ReleaseVersion = origRelease
		BuildChannel = origChannel
	}()

	Version = "v1.5.1"
	ReleaseVersion = ""
	BuildChannel = ""
	if got := EffectiveReleaseVersion(); got != "v1.5.1" {
		t.Fatalf("EffectiveReleaseVersion() = %q, want v1.5.1", got)
	}
	if got := EffectiveBuildChannel(); got != "release" {
		t.Fatalf("EffectiveBuildChannel() = %q, want release", got)
	}

	Version = "main-50cbc9fe4217"
	ReleaseVersion = ""
	BuildChannel = ""
	if got := EffectiveReleaseVersion(); got != "" {
		t.Fatalf("EffectiveReleaseVersion() = %q, want empty for non-release build", got)
	}
	if got := EffectiveBuildChannel(); got != "main" {
		t.Fatalf("EffectiveBuildChannel() = %q, want main", got)
	}

	Version = "ignored"
	ReleaseVersion = "v1.5.2"
	BuildChannel = "staging"
	if got := EffectiveReleaseVersion(); got != "v1.5.2" {
		t.Fatalf("EffectiveReleaseVersion() = %q, want explicit override", got)
	}
	if got := EffectiveBuildChannel(); got != "staging" {
		t.Fatalf("EffectiveBuildChannel() = %q, want explicit override", got)
	}
}
