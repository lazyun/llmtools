// Package procsup provides cross-platform process-tree supervision for
// commands llmTools launches on behalf of a run.
//
// On Unix a child is placed in its own process group and the whole group is
// signalled on cancellation, so grandchildren (for example the control gateway
// Pi spawns) cannot outlive the run. On Windows the equivalent is a kill-on-
// close Job Object assigned right after start: every process the tree spawns
// inherits job membership, and terminating the job ends exactly the processes
// this launch owns — never a process matched by name or started by someone
// else.
package procsup
