export type Step = { title: string; tool: string; command: string; output: string; duration: number }
export type Scenario = { prompt: string; summary: string; steps: Step[] }
const step = (title: string, tool: string, command: string, output: string, duration = 1400): Step => ({ title, tool, command, output, duration })
export const scenarios: Record<string, Scenario> = {
  local: {
    prompt: '检查这台电脑的 CPU、内存与磁盘，采样观察当前负载。',
    summary: '检查完成：8 核 CPU，16 GB 内存；当前 CPU 空闲约 82%，内存压力正常，磁盘可用 186 GB。采样期间未见持续高负载。',
    steps: [
      step('识别本机环境', '本地 Shell', 'uname -sm; sysctl -n hw.ncpu hw.memsize', 'Darwin arm64\n8\n17179869184'),
      step('检查磁盘与内存压力', '本地 Shell', 'df -h /; memory_pressure', '/dev/disk3s1  460Gi  274Gi  186Gi  60%  /\nSystem-wide memory free percentage: 42%'),
      step('持续采样 CPU · 后台任务', '本地 Shell', 'top -l 3 -s 1 -n 0 | grep "CPU usage"', 'CPU usage: 12.4% user, 5.8% sys, 81.8% idle\nCPU usage: 11.7% user, 5.5% sys, 82.8% idle\nCPU usage: 12.0% user, 5.6% sys, 82.4% idle', 3600)
    ]
  },
  ssh: {
    prompt: '检查 demo-server 的性能，观察 CPU、内存和磁盘是否存在瓶颈。',
    summary: '远端检查完成：4 核 Linux，内存可用 5.4 GiB，磁盘使用率 38%。三次采样 CPU 空闲 91–94%，暂未发现资源瓶颈。Agent 在本地运行，以上工具通过 SSH 执行。',
    steps: [
      step('识别服务器环境', 'SSH · demo-server', 'uname -sm; nproc; uptime', 'Linux x86_64\n4\n14:20:01 up 18 days, load average: 0.18, 0.23, 0.20'),
      step('检查内存和磁盘', 'SSH · demo-server', 'free -h; df -h /', 'Mem:  total 7.8Gi  used 2.1Gi  available 5.4Gi\n/dev/vda1  80G  29G  47G  38%  /'),
      step('持续观察负载 · 后台任务', 'SSH · demo-server', 'vmstat 1 3', 'r  b   swpd   free     us sy id wa\n1  0      0  4194304    6  3 91  0\n0  0      0  4202496    4  2 94  0\n0  0      0  4198400    5  2 93  0', 3600)
    ]
  },
  deploy: {
    prompt: '在本地测试并构建 demo-api，打包部署到 demo-server，检查服务是否启动成功。',
    summary: '部署完成：本地测试通过，构建产物已上传，demo-api 服务 active，健康检查 HTTP 200。当前版本 demo-20260921。',
    steps: [
      step('本地测试并交叉编译', 'Codex · 本地工具', 'go test ./... && GOOS=linux GOARCH=amd64 go build -o dist/demo-api ./cmd/api', 'ok  demo-api/internal/http  0.42s\nBuild complete: dist/demo-api (linux/amd64)', 2600),
      step('打包构建产物', 'Codex · 本地工具', 'tar -czf dist/demo-api.tar.gz -C dist demo-api', 'Artifact ready: dist/demo-api.tar.gz'),
      step('准备远端目录', 'OpenWarp MCP · shell', 'mkdir -p /srv/demo-api/releases/demo-20260921', 'Exit 0 · release directory ready'),
      step('上传发布包', 'OpenWarp MCP · sftp_upload', 'local_path: /workspace/demo-api/dist/demo-api.tar.gz\nremote_path: /srv/demo-api/releases/demo-20260921/app.tar.gz', 'Upload complete · demo-server', 2300),
      step('切换版本并重启', 'OpenWarp MCP · shell', 'cd /srv/demo-api/releases/demo-20260921 && tar -xzf app.tar.gz\nln -sfn /srv/demo-api/releases/demo-20260921 /srv/demo-api/current\nsudo systemctl restart demo-api', 'Exit 0 · service restarted', 2600),
      step('检查启动状态与日志', 'OpenWarp MCP · shell', 'systemctl is-active demo-api; curl -fsS http://127.0.0.1:8080/health\njournalctl -u demo-api -n 5 --no-pager', 'active\n{"status":"ok","version":"demo-20260921"}\nINFO listening on :8080\nINFO GET /health 200')
    ]
  },
  fix: {
    prompt: '读取 demo-server 的报错日志，在本地修复订单接口的 bug，测试后重新部署并验证。',
    summary: '修复完成：从远端日志定位空 customer 导致的 panic，本地增加校验与回归测试后重新构建部署。服务 active；无 customer 的请求返回预期 400，健康检查正常，验证窗口内未出现新的 panic。',
    steps: [
      step('读取远端异常日志', 'OpenWarp MCP · shell', 'journalctl -u demo-api -n 40 --no-pager', 'ERROR POST /orders 500\npanic: invalid memory address\ninternal/http/orders.go:42 · req.Customer.ID'),
      step('定位并修复本地代码', 'Codex · 本地修改', 'internal/http/orders.go', '+ if req.Customer == nil {\n+   http.Error(w, "customer required", http.StatusBadRequest)\n+   return\n+ }\n  customerID := req.Customer.ID', 2300),
      step('测试、编译与打包', 'Codex · 本地工具', 'go test ./... && GOOS=linux GOARCH=amd64 go build -o dist/demo-api ./cmd/api\ntar -czf dist/demo-api.tar.gz -C dist demo-api', 'PASS TestCreateOrderWithoutCustomer\nPASS TestCreateOrder\nArtifact ready: dist/demo-api.tar.gz', 2800),
      step('准备发布目录', 'OpenWarp MCP · shell', 'mkdir -p /srv/demo-api/releases/fix-customer', 'Exit 0'),
      step('上传修复版本', 'OpenWarp MCP · sftp_upload', 'local_path: /workspace/demo-api/dist/demo-api.tar.gz\nremote_path: /srv/demo-api/releases/fix-customer/app.tar.gz', 'Upload complete · demo-server', 2000),
      step('发布并重启服务', 'OpenWarp MCP · shell', 'cd /srv/demo-api/releases/fix-customer && tar -xzf app.tar.gz\nln -sfn /srv/demo-api/releases/fix-customer /srv/demo-api/current\nsudo systemctl restart demo-api', 'Exit 0 · service restarted', 2400),
      step('回归验证并重新读取日志', 'OpenWarp MCP · shell', 'systemctl is-active demo-api; curl -fsS http://127.0.0.1:8080/health\ncurl -s -o /dev/null -w "%{http_code}" -H "Content-Type: application/json" -d \'{}\' http://127.0.0.1:8080/orders\njournalctl -u demo-api --since "1 minute ago" --no-pager', 'active\n{"status":"ok"}\n400\nINFO GET /health 200\nWARN POST /orders 400 customer required\nNo new panic in verification window.')
    ]
  }
}
