package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

// update_task_list 是服务端执行的规划工具(见 docs/todo-design/):
// adapter 校验模型传来的完整任务列表后替换 conv.todoList,不转发给客户端。
// 任务列表只存在于内存,不写 conv.history、不进 conversations.json。

const updateTaskListToolName = "update_task_list"

// TaskItem 是任务清单中的一项。Priority 可选,模型可以省略。
type TaskItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"` // pending / in_progress / completed / cancelled
	Priority string `json:"priority,omitempty"`
}

const (
	taskStatusPending    = "pending"
	taskStatusInProgress = "in_progress"
	taskStatusCompleted  = "completed"
	taskStatusCancelled  = "cancelled"

	maxTaskIDLen      = 50
	maxTaskContentLen = 200
)

// updateTaskListArgs 是 update_task_list 的工具参数:全量任务列表。
type updateTaskListArgs struct {
	Tasks []TaskItem `json:"tasks"`
}

// validateTaskList 按设计方案 02 的顺序校验,任一失败返回错误。
// 校验失败时调用方不得更新 conv.todoList。
func validateTaskList(tasks []TaskItem) error {
	// 1. id 唯一
	seen := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		if _, dup := seen[t.ID]; dup {
			return fmt.Errorf("duplicate task id %q. Each task must have a unique id.", t.ID)
		}
		seen[t.ID] = struct{}{}
	}
	// 2. 单 in_progress
	var inProgress []string
	for _, t := range tasks {
		if t.Status == taskStatusInProgress {
			inProgress = append(inProgress, t.ID)
		}
	}
	if len(inProgress) > 1 {
		return fmt.Errorf("multiple in_progress tasks (%s). Only one task can be in_progress at a time.", strings.Join(inProgress, ", "))
	}
	// 3/4/5. 逐项校验 id、content、status
	for _, t := range tasks {
		if t.ID == "" {
			return fmt.Errorf("task id must not be empty.")
		}
		if len(t.ID) > maxTaskIDLen {
			return fmt.Errorf("task id %q exceeds %d characters.", t.ID, maxTaskIDLen)
		}
		if t.Content == "" {
			return fmt.Errorf("task %q content must not be empty.", t.ID)
		}
		if len(t.Content) > maxTaskContentLen {
			return fmt.Errorf("task %q content exceeds %d characters.", t.ID, maxTaskContentLen)
		}
		switch t.Status {
		case taskStatusPending, taskStatusInProgress, taskStatusCompleted, taskStatusCancelled:
		default:
			return fmt.Errorf("task %q has invalid status %q. Allowed: pending, in_progress, completed, cancelled.", t.ID, t.Status)
		}
	}
	return nil
}

// taskListResult 是成功返回给模型的 JSON 结构。
type taskListResult struct {
	OK         bool   `json:"ok"`
	ActiveTask string `json:"active_task,omitempty"`
	Completed  int    `json:"completed"`
	Pending    int    `json:"pending"`
	Total      int    `json:"total"`
}

// executeUpdateTaskList 执行一次 update_task_list 工具调用:
// 解析参数 -> 校验 -> 全量替换 conv.todoList -> 返回给模型的工具结果文本。
// 校验失败返回错误 JSON,且不更新现有状态。
func executeUpdateTaskList(conv *Conversation, tc llm.ToolCall) string {
	fail := func(format string, args ...any) string {
		raw, _ := json.Marshal(map[string]string{
			"error": fmt.Sprintf("validation failed: "+format, args...),
		})
		return string(raw)
	}
	var args updateTaskListArgs
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return fail("invalid arguments: %v", err)
	}
	if err := validateTaskList(args.Tasks); err != nil {
		return fail("%s", err.Error())
	}

	conv.todoList = args.Tasks

	res := taskListResult{OK: true, Total: len(args.Tasks)}
	for _, t := range args.Tasks {
		switch t.Status {
		case taskStatusCompleted:
			res.Completed++
		case taskStatusPending:
			res.Pending++
		case taskStatusInProgress:
			res.ActiveTask = t.ID + ": " + t.Content
		}
	}
	raw, _ := json.Marshal(res)
	return string(raw)
}

// formatTaskProgress 把当前任务清单格式化成 system prompt 尾部注入的
// "## Current Task Progress" 小节。空列表返回空串(不注入)。
func formatTaskProgress(tasks []TaskItem) string {
	if len(tasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Current Task Progress\n")
	for _, t := range tasks {
		b.WriteString("- ")
		b.WriteString(taskStatusMarker(t.Status))
		b.WriteString(" ")
		b.WriteString(t.ID)
		b.WriteString(": ")
		b.WriteString(t.Content)
		b.WriteString(" (")
		b.WriteString(t.Status)
		b.WriteString(")\n")
	}
	return b.String()
}

func taskStatusMarker(status string) string {
	switch status {
	case taskStatusCompleted:
		return "[x]"
	case taskStatusInProgress:
		return "[>]"
	case taskStatusCancelled:
		return "[-]"
	default:
		return "[ ]"
	}
}

// serverToolResult 是服务端执行的工具的结果:(tool_call_id, 结果文本),
// 直接追加到 conv.history 与 assistant tool_call 消息配对。
type serverToolResult struct {
	ID      string
	Message string
}

// taskListEqual 比较两个任务列表是否相同(用于判断是否需要推送 UI 更新)
func taskListEqual(a, b []TaskItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitServerSideToolCalls 把服务端执行的工具(update_task_list)从需要转发
// 给客户端的工具里拆出来。服务端工具必须先于客户端工具处理。
func splitServerSideToolCalls(toolCalls []llm.ToolCall) (serverSide, clientSide []llm.ToolCall) {
	for _, tc := range toolCalls {
		if tc.Name == updateTaskListToolName {
			serverSide = append(serverSide, tc)
			continue
		}
		clientSide = append(clientSide, tc)
	}
	return serverSide, clientSide
}

// todoListToProto 把内存里的 TaskItem 列表转成 proto 消息,推给 Warp 客户端刷新 todo 面板。
// 规则:
// - 首次调用(之前没有 todoList) -> CreateTodoList
// - 有 pending 项变化 -> UpdatePendingTodos(传当前 pending 列表)
// - 有项标成 completed -> MarkTodosCompleted(传刚完成的 id 列表)
// 简化:每次都发 UpdatePendingTodos + MarkTodosCompleted 的组合。
func todoListToProto(prev, curr []TaskItem) []*pb.Message_UpdateTodos {
	var ops []*pb.Message_UpdateTodos

	// 构建 prev 状态 map 用于 diff
	prevStatus := make(map[string]string, len(prev))
	for _, t := range prev {
		prevStatus[t.ID] = t.Status
	}

	var pendingItems []*pb.TodoItem
	var completedIDs []string
	for _, t := range curr {
		if t.Status == taskStatusCompleted && prevStatus[t.ID] != taskStatusCompleted {
			completedIDs = append(completedIDs, t.ID)
		}
		if t.Status != taskStatusCompleted && t.Status != taskStatusCancelled {
			pendingItems = append(pendingItems, &pb.TodoItem{
				Id:          t.ID,
				Title:       t.Content,
				Description: t.Priority,
			})
		}
	}

	if len(prev) == 0 && len(curr) > 0 {
		// 首次创建
		ops = append(ops, &pb.Message_UpdateTodos{
			Operation: &pb.Message_UpdateTodos_CreateTodoList{
				CreateTodoList: &pb.CreateTodoList{InitialTodos: pendingItems},
			},
		})
	} else {
		if len(pendingItems) > 0 || len(prev) != len(curr) {
			ops = append(ops, &pb.Message_UpdateTodos{
				Operation: &pb.Message_UpdateTodos_UpdatePendingTodos{
					UpdatePendingTodos: &pb.UpdatePendingTodos{UpdatedPendingTodos: pendingItems},
				},
			})
		}
	}
	if len(completedIDs) > 0 {
		ops = append(ops, &pb.Message_UpdateTodos{
			Operation: &pb.Message_UpdateTodos_MarkTodosCompleted{
				MarkTodosCompleted: &pb.MarkTodosCompleted{TodoIds: completedIDs},
			},
		})
	}
	return ops
}
