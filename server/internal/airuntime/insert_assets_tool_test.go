package airuntime

import "testing"

func TestARTEXAssetInputKeepsOriginalFieldNames(t *testing.T) {
	asset, err := decodeARTEXAsset([]byte(`{"type":"endpoint","target_id":7,"url":"https://example.com/a","method":"POST","page_title":"title","status_code":200,"auth":[{"token":"synthetic"}]}`))
	if err != nil || asset.TargetID != 7 || asset.PageTitle != "title" || asset.StatusCode == nil || *asset.StatusCode != 200 || len(asset.Auth) != 1 {
		t.Fatalf("ARTEX mapping: %+v %v", asset, err)
	}
	if _, err := decodeARTEXAsset([]byte(`{"type":"ip","target_id":7,"targetId":8}`)); err == nil {
		t.Fatal("ambiguous input aliases accepted")
	}
	if _, err := decodeARTEXAsset([]byte(`{"type":"ip","unknown_field":true}`)); err == nil {
		t.Fatal("unknown input field was silently dropped")
	}
}
