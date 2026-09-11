package websocket

import "testing"

func TestValidateRoom(t *testing.T) {
	valid := []string{
		"exec:inst_123:exec_456",
		"exec:inst_1:exec_2",
		"inst:inst_123",
		"room",
	}
	for _, room := range valid {
		if err := validateRoom(room); err != nil {
			t.Errorf("validateRoom(%q) = %v, want nil", room, err)
		}
	}

	invalid := []string{
		"",
		"exec:",
		":inst:exec",
		"exec::exec_1",
		"exec:inst_1:",
		"bad room",
		"exec:inst:exec with space",
	}
	for _, room := range invalid {
		if err := validateRoom(room); err == nil {
			t.Errorf("validateRoom(%q) = nil, want error", room)
		}
	}
}
