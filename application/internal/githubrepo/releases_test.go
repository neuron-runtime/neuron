package githubrepo

import "testing"

func TestParseTagToVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2.0":  "1.2.0",
		"1.2.0":   "1.2.0",
		" v2.0.0": "2.0.0",
		"":        "",
	}
	for in, want := range cases {
		if got := ParseTagToVersion(in); got != want {
			t.Errorf("ParseTagToVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionsFromReleasesPrefersStable(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.0-beta.1", Prerelease: true},
		{TagName: "v1.1.0"},
		{TagName: "v1.0.0"},
	}
	got := VersionsFromReleases(releases)
	want := []string{"1.1.0", "1.0.0"}
	if len(got) != len(want) {
		t.Fatalf("VersionsFromReleases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("VersionsFromReleases = %v, want %v", got, want)
		}
	}
}

func TestVersionsFromReleasesFallsBackToPrerelease(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.0-beta.1", Prerelease: true},
		{TagName: "v2.0.0-alpha.1", Prerelease: true},
	}
	got := VersionsFromReleases(releases)
	if len(got) != 2 || got[0] != "2.0.0-beta.1" {
		t.Fatalf("VersionsFromReleases = %v, want prereleases newest first", got)
	}
}

func TestAssetByName(t *testing.T) {
	r := &Release{Assets: []Asset{{Name: "a"}, {Name: "b"}}}
	if a, ok := r.AssetByName("b"); !ok || a.Name != "b" {
		t.Fatalf("AssetByName(b) = %+v, %v", a, ok)
	}
	if _, ok := r.AssetByName("c"); ok {
		t.Fatal("AssetByName(c) must miss")
	}
}
