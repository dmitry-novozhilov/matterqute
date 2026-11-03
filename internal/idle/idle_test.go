package idle

import "testing"

// Since не падает без дисплея и DBus; если знает простой — он не отрицательный.
func TestSince(t *testing.T) {
	d, ok := Since()
	if ok && d < 0 {
		t.Errorf("idle %v", d)
	}
	t.Logf("idle %v, known %v", d, ok)
}
