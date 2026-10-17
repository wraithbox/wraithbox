package vm

// SetGeneration sets slot's recorded generation, so a test can reach
// the generation checks of Stop and ConnectGuest with 0, which Start
// never hands out.
func SetGeneration(m *Manager, slot Slot, g uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slots[slot].generation = g
}
