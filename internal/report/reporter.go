package report

// Reporter is the ONE unified status backchannel port, consumed by ALL five runners (tf plus
// the four ports: manual/gitlab/azdevops/github) — there is no separate "event-driven"
// reporter for the ported runners, they use this same interface and simply discard the abort
// return.
//
// Report transmits only the steps present in RunStatus.Steps — the changed/new steps since the
// caller's last send. The meshfed runner-facing status endpoint upserts steps by id, so sending
// a subset is safe, and each included step must carry its FULL current message text: the
// backend overwrites userMessage/systemMessage by assignment, never by appending, so a partial
// message is never safe to send as a "delta".
type Reporter interface {
	Register(RunStatus) error
	Report(RunStatus) (abort bool, err error)
}
