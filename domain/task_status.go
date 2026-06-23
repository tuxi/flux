package domain

var AllowedTransitionsTasks = map[TaskStatus][]TaskStatus{
	TaskPending:   {TaskRunning, TaskCanceled},
	TaskRunning:   {TaskSuspended, TaskSuccess, TaskFailed, TaskCanceled},
	TaskSuspended: {TaskRunning, TaskFailed, TaskCanceled},
}

func IsAllowedTaskTransition(from, to TaskStatus) bool {
	for _, candidate := range AllowedTransitionsTasks[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
