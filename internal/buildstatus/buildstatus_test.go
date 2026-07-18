package buildstatus

import (
	"testing"
	"time"
)

func TestLastReportsZeroValueUntilFirstRecord(t *testing.T) {
	ResetForTest()
	snap := Last()
	if snap.Attempted {
		t.Fatalf("Last() before any record = %+v, want Attempted=false", snap)
	}
}

func TestRecordSuccessThenFailureOverwritesSnapshot(t *testing.T) {
	ResetForTest()
	t1 := time.Now().Add(-time.Hour)
	RecordSuccess(t1)
	snap := Last()
	if !snap.Attempted || snap.Status != "ok" || !snap.At.Equal(t1) {
		t.Fatalf("Last() after RecordSuccess = %+v", snap)
	}

	t2 := time.Now()
	RecordFailure("permission_denied", t2)
	snap = Last()
	if !snap.Attempted || snap.Status != "failed" || snap.ErrorClass != "permission_denied" || !snap.At.Equal(t2) {
		t.Fatalf("Last() after RecordFailure = %+v", snap)
	}
}
