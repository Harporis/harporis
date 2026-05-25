package scan

// waitFirstSpawn waits until either one worker reports success (true) on
// spawnSig or until all `workers` workers have reported (each sending one
// bool). Returns true iff at least one worker spawned successfully.
//
// This replaces a previous hard-coded 100ms watchdog that would falsely
// cancel the walker if no worker had managed to spawn its cat-file
// subprocess within that window — a fragile assumption on cold containers.
func waitFirstSpawn(spawnSig <-chan bool, workers int) bool {
	if workers <= 0 {
		return false
	}
	failed := 0
	for {
		ok, more := <-spawnSig
		if !more {
			return false
		}
		if ok {
			return true
		}
		failed++
		if failed >= workers {
			return false
		}
	}
}
