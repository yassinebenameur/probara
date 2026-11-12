package pagination

import "testing"

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ page, size, wantPage, wantSize int }{
		{1, 20, 1, 20},
		{0, 0, 1, DefaultSize},
		{-3, -1, 1, DefaultSize},
		{4, 100, 4, 100},
		{2, 101, 2, MaxSize},
		{2, 5000, 2, MaxSize},
	} {
		page, size := Normalize(tc.page, tc.size, DefaultSize, MaxSize)
		if page != tc.wantPage || size != tc.wantSize {
			t.Errorf("Normalize(%d, %d) = %d, %d; want %d, %d", tc.page, tc.size, page, size, tc.wantPage, tc.wantSize)
		}
	}
	if got := Offset(3, 20); got != 40 {
		t.Errorf("Offset(3, 20) = %d, want 40", got)
	}
}
