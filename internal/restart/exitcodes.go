package restart

// ExitCodeRestartRequested is the process exit code used to signal a supervising
// parent that the child requested a restart.
//
// Keep this stable. Supervisors compare against it to decide whether to respawn.
const ExitCodeRestartRequested = 23
