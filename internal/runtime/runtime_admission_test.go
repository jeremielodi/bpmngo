package runtime

import "testing"

func TestAdmissionLimitForBacklog(t *testing.T) {
	a := &admissionController{
		baseLimit:    16,
		minimumLimit: 3,
		softBacklog:  100,
		hardBacklog:  500,
	}
	tests := []struct {
		name    string
		backlog int
		want    int
	}{
		{"empty", 0, 16},
		{"below soft", 99, 16},
		{"at soft", 100, 8},
		{"between thresholds", 499, 8},
		{"at hard", 500, 3},
		{"above hard", 5000, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.limitForBacklog(tt.backlog); got != tt.want {
				t.Fatalf("limitForBacklog(%d) = %d, want %d", tt.backlog, got, tt.want)
			}
		})
	}
}

func TestAdmissionLimitNeverFallsBelowMinimum(t *testing.T) {
	a := &admissionController{
		baseLimit:    4,
		minimumLimit: 3,
		softBacklog:  10,
		hardBacklog:  20,
	}
	if got := a.limitForBacklog(10); got != 3 {
		t.Fatalf("soft-limit result = %d, want minimum 3", got)
	}
}
