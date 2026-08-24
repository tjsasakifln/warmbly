package handler

import "testing"

func TestConfengeReviewPageHonorsBoundedPagination(t *testing.T) {
	tests := []struct {
		name       string
		limitRaw   string
		offsetRaw  string
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", wantLimit: 100, wantOffset: 0},
		{name: "requested page", limitRaw: "200", offsetRaw: "50000", wantLimit: 200, wantOffset: 50000},
		{name: "invalid bounds", limitRaw: "201", offsetRaw: "100001", wantLimit: 100, wantOffset: 0},
		{name: "negative", limitRaw: "-1", offsetRaw: "-1", wantLimit: 100, wantOffset: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limit, offset := confengeReviewPage(test.limitRaw, test.offsetRaw)
			if limit != test.wantLimit || offset != test.wantOffset {
				t.Fatalf("page=(%d,%d), want (%d,%d)", limit, offset, test.wantLimit, test.wantOffset)
			}
		})
	}
}
