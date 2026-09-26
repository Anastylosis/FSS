package scraper

import "testing"

func TestWorkerCount(t *testing.T) {
	tests := []struct {
		name string
		opts ListOpts
		def  int
		want int
	}{
		{"operator wins", ListOpts{Workers: 16}, 4, 16},
		{"operator can shrink the pool", ListOpts{Workers: 1}, 8, 1},
		{"unset falls back to the scraper's default", ListOpts{}, 4, 4},
		{"a negative request is not a request", ListOpts{Workers: -3}, 4, 4},
		{"never zero", ListOpts{}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkerCount(tt.opts, tt.def); got != tt.want {
				t.Errorf("WorkerCount = %d, want %d", got, tt.want)
			}
		})
	}
}
