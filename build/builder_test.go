package build

import "testing"

func TestOptions(t *testing.T) {
	t.Run("LargeFiles", func(t *testing.T) {
		opts := Options{
			LargeFiles: []string{"/foo", "*.foo", "/foo/bar/*.foo"},
		}

		tests := []struct {
			name   string
			ignore bool
		}{
			{name: "/foo", ignore: true},
			{name: "/foo/foo", ignore: false},
			{name: ".foo", ignore: true},
			{name: "foo.foo", ignore: true},
			{name: "/foo/foo", ignore: false},
			{name: "/foo/bar/bar.foo", ignore: true},
			{name: "/foo/bar/.foo", ignore: true},
			{name: "/foo/bar/foo", ignore: false},
		}

		for _, test := range tests {
			ignore := opts.IgnoreSizeMax(test.name)
			if ignore != test.ignore {
				t.Errorf("unexpected result for name %s: got %v, want %v", test.name, ignore, test.ignore)
			}
		}
	})
}
