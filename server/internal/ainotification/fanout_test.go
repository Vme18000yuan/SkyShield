package ainotification

import (
	"reflect"
	"testing"
)

func TestDecodeSnapshotSupportsUnifiedAndARTEXAssetIDs(t *testing.T) {
	snapshot, err := decodeSnapshot([]byte(`{"kind":"finding_created","task_id":1,"finding_id":2,"asset_ids":[1,"2",-3,"wrong",null,1.5]}`))
	if err != nil || !reflect.DeepEqual(snapshot.AssetIDs, []int64{1, 2}) {
		t.Fatalf("decoded assets = %v, error = %v", snapshot.AssetIDs, err)
	}
	if _, err = decodeSnapshot([]byte(`{"kind":"unexpected","task_id":1,"finding_id":2}`)); err == nil {
		t.Fatal("unknown event kind accepted")
	}
}
