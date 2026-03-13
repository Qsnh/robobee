# Bee Log to File

## Summary

将 bee 子进程（claude CLI）的 stdout/stderr 原始输出记录到独立日志文件，替代现有的 `log.Printf` 打印。

## Requirements

- 日志路径：`~/.robobee/bee-logs/{session_id}_{timestamp}.log`
- 每次 bee 运行生成独立文件
- timestamp 格式：`20060102_150405`
- stdout 行加 `[stdout]` 前缀，stderr 行加 `[stderr]` 前缀
- 移除现有的 `log.Printf("bee: ...")` 和 `log.Printf("bee stderr: ...")` 打印
- 目录硬编码，不可配置
- 无自动清理机制

## Design

### 改动文件

`internal/bee/bee_process.go` — `Run()` 方法

### 改动内容

1. `Run()` 开始时，构造日志文件路径：`~/.robobee/bee-logs/{sessionID}_{time.Now().Format("20060102_150405")}.log`
2. `os.MkdirAll` 创建目录
3. `os.OpenFile` 以 `O_CREATE|O_WRONLY` 打开日志文件
4. stdout/stderr goroutine 中用 `fmt.Fprintf(logFile, "[stdout] %s\n", line)` / `fmt.Fprintf(logFile, "[stderr] %s\n", line)` 写入文件
5. 移除 `log.Printf` 调用
6. 用 `sync.WaitGroup` 等待两个 goroutine 完成后再关闭文件
7. `defer logFile.Close()` 确保文件关闭

### 无 sessionID 的情况

如果 `sessionID` 为空，使用 `"nosession"` 作为文件名前缀。

### 不涉及的改动

- 不改 config 结构
- 不改 feeder 逻辑
- 不加清理/轮转机制
