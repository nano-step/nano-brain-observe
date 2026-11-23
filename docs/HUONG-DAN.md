# Hướng dẫn sử dụng nano-brain-observe

`nano-brain-observe` là một proxy đứng trước nano-brain. Mỗi lần agent gọi
nano-brain (tool nào, tham số gì, kết quả ra sao, mất bao lâu), proxy ghi lại
một dòng JSON vào file. Dữ liệu này dùng để tìm chỗ nano-brain còn yếu: tool
nào hay trả rỗng, tool nào chậm, lỗi nào lặp lại.

Đây là công cụ tùy chọn, giống một plugin: có chạy proxy và agent trỏ vào nó
thì có dữ liệu; không thì mọi thứ như cũ. nano-brain không bị sửa gì.

```
agent  --->  nano-brain-observe (127.0.0.1:3101)  --->  nano-brain (127.0.0.1:3100)
                       |
                       v
        ~/.nano-brain-observe/logs/calls-YYYY-MM-DD.jsonl
```

## 1. Cài đặt

Cần Node 18 trở lên. Gói npm tự tải binary đúng nền tảng (macOS, Linux,
Windows; x64 và arm64) từ GitHub release và kiểm tra SHA-256.

```sh
npm install -g nano-brain-observe
nano-brain-observe --version
```

Chạy thử không cần cài:

```sh
npx nano-brain-observe
```

Hoặc build từ source (chỉ cần Go):

```sh
go build -o nano-brain-observe ./cmd/nano-brain-observe
```

## 2. Chạy proxy

nano-brain phải đang chạy ở cổng 3100.

```sh
nano-brain-observe
```

Mọi tùy chọn đều có cờ; chỉ những dòng có biến môi trường bên dưới mới
có thể đặt qua biến môi trường:

| Cờ | Biến môi trường | Mặc định | Ý nghĩa |
|---|---|---|---|
| `--listen` | `NANO_BRAIN_OBSERVE_LISTEN` | `127.0.0.1:3101` | Địa chỉ proxy lắng nghe |
| `--upstream` | `NANO_BRAIN_OBSERVE_UPSTREAM` | `http://127.0.0.1:3100` | Địa chỉ nano-brain |
| `--dir` | `NANO_BRAIN_OBSERVE_DIR` | `~/.nano-brain-observe/logs` | Thư mục chứa log |
| `--max-response-bytes` | `NANO_BRAIN_OBSERVE_MAX_RESPONSE_BYTES` | `262144` | Giới hạn kích thước response được lưu (256 KiB) |
| `--allowed-hosts` | — | rỗng | Tên host/IP được phép thêm (khớp chính xác, không gồm cổng) |
| `--unsafe-expose-lan` | — | tắt | Cho phép lắng nghe ngoài loopback (proxy không có xác thực) |

Nếu OpenCode dùng URL `host.docker.internal` và truy cập được proxy qua
địa chỉ loopback mặc định, chạy:

```sh
nano-brain-observe --allowed-hosts host.docker.internal
```

Không cần `--unsafe-expose-lan` trong trường hợp này. Nếu thiếu
`--allowed-hosts`, kết nối vẫn có thể thành công nhưng proxy trả HTTP 403.
Nếu container không kết nối được với địa chỉ loopback, phải mở cổng trên
giao diện mạng mà container truy cập được; khi đó dùng **cả hai** cờ:

```sh
nano-brain-observe --listen 0.0.0.0:3101 \
  --unsafe-expose-lan --allowed-hosts host.docker.internal
```

Proxy không có xác thực: khi mở cổng ra LAN, máy khác có thể giả mạo Host
được cho phép để gọi nano-brain. Ưu tiên bind một giao diện cụ thể và chặn
truy cập bằng tường lửa; xem [AGENTS-SETUP.md](AGENTS-SETUP.md#opencode).

Muốn proxy tự chạy khi đăng nhập macOS: xem mẫu launchd trong
[AGENTS-SETUP.md](AGENTS-SETUP.md#optional-run-at-login-on-macos).

## 3. Trỏ agent vào proxy

Trong cấu hình MCP của từng agent, đổi URL nano-brain từ cổng `3100` sang
`3101`, giữ nguyên đường dẫn `/mcp` và tham số `?workspace=` nếu có.

| Agent | File cấu hình | Mục cần sửa |
|---|---|---|
| Claude Code | `~/.claude.json` | `"nano-brain": {"type": "http", "url": "http://localhost:3101/mcp"}` (sửa mọi chỗ xuất hiện) |
| Codex CLI | `~/.codex/config.toml` | `[mcp_servers.nano-brain]` → `url = "http://localhost:3101/mcp"` |
| Pi | `~/.pi/agent/mcp.json` | `"nano-brain"` → `"url": "http://localhost:3101/mcp"` |
| OpenCode | `~/.config/opencode/opencode.json` | `"nano-brain"` → `"url": "http://host.docker.internal:3101/mcp"` (chạy proxy với `--allowed-hosts host.docker.internal` như trên) |
| Devin | theo nguồn mà `devin mcp list` báo | thường lấy từ `~/.claude.json`, nên sửa Claude Code là đủ |

Sau khi sửa, mở lại phiên agent: phiên đang mở vẫn giữ kết nối cũ. Chi tiết
từng agent và cách hoàn tác: [AGENTS-SETUP.md](AGENTS-SETUP.md).

## 4. Kiểm tra đã có dữ liệu

Dùng agent gọi nano-brain một lần, rồi:

```sh
jq -r 'select(.kind=="mcp_tool") | [.ts, .client, .tool, .status, .latency_ms] | @tsv' \
  ~/.nano-brain-observe/logs/calls-*.jsonl | tail
```

Mỗi tool call là một dòng. Các trường chính:

| Trường | Ý nghĩa |
|---|---|
| `kind` | `mcp_tool` (tool call), `mcp` (bắt tay MCP), `rest` (HTTP khác) |
| `client` | Tên và phiên bản agent |
| `tool`, `args` | Tool được gọi và tham số |
| `status` | `ok`, `empty` (thành công nhưng không có kết quả), `error` |
| `result_count` | Số kết quả, nếu suy ra được |
| `latency_ms` | Thời gian xử lý |
| `response` | Nội dung trả về (bị cắt nếu vượt giới hạn) |

## 5. Phân tích để cải thiện nano-brain

Số lần gọi theo tool:

```sh
jq -r 'select(.kind=="mcp_tool") | .tool' ~/.nano-brain-observe/logs/calls-*.jsonl | sort | uniq -c | sort -rn
```

Tỉ lệ trả rỗng theo tool (tool, số lần gọi, số lần rỗng, phần trăm):

```sh
jq -rs 'map(select(.kind=="mcp_tool")) | group_by(.tool)[] | (map(select(.status=="empty")) | length) as $e | "\(.[0].tool)\t\(length)\t\($e)\t\($e * 100 / length | round)%"' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Các tool call bị lỗi, kèm tham số:

```sh
jq -c 'select(.kind=="mcp_tool" and .status=="error") | {ts, client, tool, args, error}' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Độ trễ p50 / p95 theo tool (ms):

```sh
jq -rs 'map(select(.kind=="mcp_tool")) | group_by(.tool)[] | (map(.latency_ms) | sort) as $l | ($l | length) as $n | "\(.[0].tool)\tn=\($n)\tp50=\($l[($n * 0.5 | ceil) - 1])\tp95=\($l[($n * 0.95 | ceil) - 1])"' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Luôn lọc `kind=="mcp_tool"` khi tính tỉ lệ lỗi: một số agent dò các đường dẫn
OAuth `/.well-known/...` lúc khởi động, nano-brain trả 404 và chúng được ghi
thành dòng `rest` với trạng thái `error`.

## 6. Quyền riêng tư

Log chứa toàn bộ câu truy vấn và nội dung nano-brain trả về, kể cả từ các
workspace riêng tư. Thư mục log có quyền 0700, file 0600. Không commit và
không chia sẻ các file này.

## 7. Gỡ bỏ

1. Đổi URL của từng agent về lại cổng 3100.
2. Dừng proxy (Ctrl-C, hoặc gỡ launchd job nếu đã cài).
3. Nếu muốn, xóa log: `rm -rf ~/.nano-brain-observe`.

Làm bước 1 trước: agent còn trỏ vào 3101 sau khi proxy dừng sẽ mất kết nối
nano-brain.
