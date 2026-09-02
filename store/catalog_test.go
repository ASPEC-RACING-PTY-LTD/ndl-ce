package storecatalog

import "testing"

func TestOfficialSampleParses(t *testing.T) {
	files, err := Official()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Manifest.Name != "sample-web" || files[0].Manifest.Class != "official" {
		t.Fatalf("%+v", files)
	}
}
