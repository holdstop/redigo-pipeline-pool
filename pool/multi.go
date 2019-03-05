package pool

type Multi struct {
	cmds []cmdObj
}

func (multi *Multi) Do(cmd string, args ...interface{}) {
	multi.cmds = append(multi.cmds, cmdObj{cmd, args})
}