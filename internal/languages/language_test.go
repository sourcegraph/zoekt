package languages

import "testing"

func TestGetLanguageByAlias(t *testing.T) {
	tests := []struct {
		name   string
		alias  string
		want   string
		wantOk bool
	}{
		{
			name:   "empty alias",
			alias:  "",
			want:   "",
			wantOk: false,
		},
		{
			name:   "unknown alias",
			alias:  "unknown",
			want:   "",
			wantOk: false,
		},
		{
			name:   "supported alias",
			alias:  "go",
			want:   "Go",
			wantOk: true,
		},
		{
			name:   "unsupported by linguist alias",
			alias:  "magik",
			want:   "Magik",
			wantOk: true,
		},
		{
			name:   "unsupported by linguist alias normalized",
			alias:  "mAgIk",
			want:   "Magik",
			wantOk: true,
		},
		{
			name:   "apex example unsupported by linguist alias",
			alias:  "apex",
			want:   "Apex",
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GetLanguageByAlias(tt.alias)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("GetLanguageByAlias(%q) = %q, %t, want %q, %t", tt.alias, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

func TestGetLanguage(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  []byte
		want     string
	}{
		{
			name:     "empty filename",
			filename: "",
			content:  []byte(""),
			want:     "",
		},
		{
			name:     "unknown extension",
			filename: "file.unknown",
			content:  []byte(""),
			want:     "",
		},
		{
			name:     "supported extension",
			filename: "file.go",
			content:  []byte("package main"),
			want:     "Go",
		},
		{
			name:     "magik: unsupported by linguist extension",
			filename: "file.magik",
			content:  []byte(""),
			want:     "Magik",
		},
		{
			name:     "apex: unsupported by linguist extension",
			filename: "file.apxc",
			content:  []byte(""),
			want:     "Apex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLanguage(tt.filename, tt.content)
			if got != tt.want {
				t.Errorf("GetLanguage(%q, %q) = %q, want %q", tt.filename, tt.content, got, tt.want)
			}
		})
	}
}
