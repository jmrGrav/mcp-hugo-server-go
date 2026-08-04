package write

// export_test.go exposes internal test seams to the external write_test
// package. It is compiled only under `go test`, so it adds nothing to the
// package's public API.

// SetApplyBundleWriteHook installs (or clears, with nil) the apply_bundle_plan
// per-write failure seam used by #854 AC5's "interruption during apply"
// regression test. Returns a restore func the test should defer.
func SetApplyBundleWriteHook(fn func(index int) error) func() {
	prev := applyBundleWriteHook
	applyBundleWriteHook = fn
	return func() { applyBundleWriteHook = prev }
}
