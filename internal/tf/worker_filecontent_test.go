package tf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileContentOrEmpty(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "log.txt")
	content := "0123456789"
	if err := os.WriteFile(logFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	cases := []struct {
		name     string
		fileName string
		startIdx int64
		endIdx   int64
		want     string
	}{
		{
			name:     "normal sub-range",
			fileName: logFile,
			startIdx: 2,
			endIdx:   6,
			want:     "2345",
		},
		{
			name:     "endIdx past file size clamps to available bytes",
			fileName: logFile,
			startIdx: 5,
			endIdx:   1000,
			want:     "56789",
		},
		{
			name:     "startIdx == endIdx returns empty",
			fileName: logFile,
			startIdx: 3,
			endIdx:   3,
			want:     "",
		},
		{
			name:     "startIdx > endIdx returns empty",
			fileName: logFile,
			startIdx: 6,
			endIdx:   3,
			want:     "",
		},
		{
			name:     "nonexistent file returns empty",
			fileName: filepath.Join(dir, "does-not-exist.txt"),
			startIdx: 0,
			endIdx:   5,
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fileContentOrEmpty(tc.fileName, tc.startIdx, tc.endIdx)
			if got != tc.want {
				t.Errorf("fileContentOrEmpty(%q, %d, %d) = %q, want %q", tc.fileName, tc.startIdx, tc.endIdx, got, tc.want)
			}
		})
	}
}
