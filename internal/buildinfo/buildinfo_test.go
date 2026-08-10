package buildinfo

import (
	"runtime/debug"
	"testing"
)

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

func TestEffectiveBuildChannelFallbacks(t *testing.T) {
	origVersion := Version
	origRelease := ReleaseVersion
	origChannel := BuildChannel
	defer func() {
		Version = origVersion
		ReleaseVersion = origRelease
		BuildChannel = origChannel
	}()

	Version = "custom-build"
	ReleaseVersion = ""
	BuildChannel = ""
	if got := EffectiveBuildChannel(); got != "custom" {
		t.Fatalf("EffectiveBuildChannel() = %q, want custom", got)
	}

	Version = "   "
	ReleaseVersion = ""
	BuildChannel = ""
	if got := EffectiveBuildChannel(); got != "dev" {
		t.Fatalf("EffectiveBuildChannel() = %q, want dev", got)
	}
}

func TestEffectiveReleaseVersionTrimsWhitespaceOverride(t *testing.T) {
	origVersion := Version
	origRelease := ReleaseVersion
	origChannel := BuildChannel
	defer func() {
		Version = origVersion
		ReleaseVersion = origRelease
		BuildChannel = origChannel
	}()

	Version = "main-deadbeef"
	ReleaseVersion = "  v1.7.9  "
	BuildChannel = ""

	if got := EffectiveReleaseVersion(); got != "v1.7.9" {
		t.Fatalf("EffectiveReleaseVersion() = %q, want trimmed v1.7.9", got)
	}
	if got := EffectiveBuildChannel(); got != "main" {
		t.Fatalf("EffectiveBuildChannel() = %q, want main because explicit release version does not force release unless Version matches", got)
	}
}

func TestEffectiveBuildChannelFallsBackToWholeTrimmedVersionWhenPrefixEmpty(t *testing.T) {
	origVersion := Version
	origRelease := ReleaseVersion
	origChannel := BuildChannel
	defer func() {
		Version = origVersion
		ReleaseVersion = origRelease
		BuildChannel = origChannel
	}()

	Version = "  -dirty-build  "
	ReleaseVersion = ""
	BuildChannel = "   "

	if got := EffectiveBuildChannel(); got != "-dirty-build" {
		t.Fatalf("EffectiveBuildChannel() = %q, want fallback to full trimmed version", got)
	}
}

func TestApplyBuildInfoParsesSettings(t *testing.T) {
	origCommit := Commit
	origCommitTime := CommitTime
	origDirty := Dirty
	defer func() {
		Commit = origCommit
		CommitTime = origCommitTime
		Dirty = origDirty
	}()

	Commit = ""
	CommitTime = ""
	Dirty = false

	applyBuildInfo(&debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-08-09T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "ignored", Value: "x"},
		},
	}, true)

	if Commit != "abc123" || CommitTime != "2026-08-09T10:00:00Z" || !Dirty {
		t.Fatalf("applyBuildInfo() = Commit=%q CommitTime=%q Dirty=%v, want parsed values", Commit, CommitTime, Dirty)
	}
}

func TestApplyBuildInfoNoBuildInfoLeavesGlobalsUntouched(t *testing.T) {
	origCommit := Commit
	origCommitTime := CommitTime
	origDirty := Dirty
	defer func() {
		Commit = origCommit
		CommitTime = origCommitTime
		Dirty = origDirty
	}()

	Commit = "keep"
	CommitTime = "keep-time"
	Dirty = true
	applyBuildInfo(nil, false)

	if Commit != "keep" || CommitTime != "keep-time" || !Dirty {
		t.Fatalf("applyBuildInfo(nil,false) should leave globals untouched, got Commit=%q CommitTime=%q Dirty=%v", Commit, CommitTime, Dirty)
	}
}
