package languages

import (
	_ "embed"
	"testing"
)

//go:embed testdata/SageTreeBuilder.C
var embeddedSageTreeBuilder []byte

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
		{
			name:     "C++ file with .C extension - real SageTreeBuilder.C content",
			filename: "SageTreeBuilder.C",
			content:  embeddedSageTreeBuilder,
			want:     "C++",
		},
		{
			name:     "C file with .C extension - should remain C",
			filename: "test.C",
			content: []byte(`#include <stdio.h>
#include <stdlib.h>

int main() {
    printf("Hello, World!\n");
    return 0;
}
`),
			want: "C",
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

// Benchmarks to verify that C++ override logic has acceptable performance overhead
func BenchmarkGetLanguage(b *testing.B) {
	// Use embedded SageTreeBuilder.C file for benchmarking
	sageBuildContent := embeddedSageTreeBuilder

	benchmarks := []struct {
		name     string
		filename string
		content  []byte
	}{
		{
			name:     "C_file_no_override",
			filename: "test.c",
			content:  []byte("#include <stdio.h>\nint main() { return 0; }"),
		},
		{
			name:     "Cpp_file_normal_extension",
			filename: "test.cpp",
			content:  sageBuildContent,
		},
		{
			name:     "C_file_capital_C_extension_trigger_override",
			filename: "test.C",
			content:  []byte("#include <stdio.h>\nint main() { return 0; }"),
		},
		{
			name:     "Cpp_file_capital_C_extension_with_override",
			filename: "SageTreeBuilder.C",
			content:  sageBuildContent,
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				GetLanguage(bm.filename, bm.content)
			}
		})
	}
}
