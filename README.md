# 2026 国考 · 行政执法类冲刺手册

Go + SQLite 的服务端渲染应用，前后端不分离：页面由 `html/template` 直接渲染，学习进度和考试日期写入 SQLite，可跨浏览器、跨设备同步。使用 Docker Compose 部署。

## 快速部署

```bash
docker compose up -d --build
```

浏览器打开 `http://<服务器IP>:8080`。首次启动会自动建库、建表。

- 数据保存在命名卷 `gongkao-study-data` 的 `/data/study.db`，删除或重建容器不会丢数据。
- 如 8080 被占用，复制 `.env.example` 为 `.env`，修改 `APP_PORT` 后重新 `docker compose up -d`。
- 查看日志：`docker compose logs -f app`；健康检查：`curl http://localhost:8080/healthz`。

## 本地运行（不用 Docker）

需要 Go 1.24+。Windows 上 8080 常被其他软件占用，项目自带 `run.cmd`，默认用 8090：

```bat
run.cmd
```

也可以手动指定端口：

```bat
set ADDR=127.0.0.1:8090
go run .
```

Linux/macOS：

```bash
ADDR=127.0.0.1:8090 go run .
```

默认数据库为 `data/study.db`，可用 `DB_PATH` 修改。

## 手机访问

`run.cmd` 默认监听 `:8090`，同一 Wi-Fi 下的手机可以直接访问：

1. 电脑上执行 `ipconfig`，记下 IPv4 地址（如 `192.168.1.23`）；
2. 手机浏览器打开 `http://192.168.1.23:8090`；
3. 首次运行时 Windows 防火墙若弹窗，选择允许“专用网络”。

如果只想本机使用，把 `run.cmd` 里的 `ADDR=:8090` 改成 `ADDR=127.0.0.1:8090`。
监听 `:8090` 会暴露到局域网，请只在可信网络使用；公网访问请加反向代理和访问认证。

## 功能

- **160 个知识点勾选**：勾选即写入 SQLite，刷新、换设备都保留；页面顶部显示全局完成数与进度条。
- **分页阅读**：总览/考情合并为首页，其余十个章节各自一页；每页底部有上一章/下一章按钮，侧栏和汉堡菜单直达。
- **零基础提示**：每章开头用几句话解释本章核心名词和例子，正文仍保留表格、口诀和例题，避免只有结论看不懂。
- **章节练习（48 题）**：8 个章节各有 4～8 题，以 2025 国考行测（地市级）真题为主，辅以按考点编写的模拟题；
  每章正文底部有“本章练习”入口，并显示已做/答对数量。提交后服务端判分，立即显示正确答案、你的选择和解析；
  做错时解析自动进入视野。记录已做/正确率，可按章节重置。
- **考试倒计时**：默认 2026-11-29，按开考 09:00 精确到秒（天 + 时:分:秒），页面可直接改日期，保存到服务端。
- **搜索 / 只看未掌握 / 打印**：沿用原静态页的交互，打印时自动展开折叠内容。
- **移动端导航**：窄屏右上角汉堡菜单，点开为两列章节入口；选择后自动收起并打开对应页面。
- **26 道例题**：资料、数量、判断、言语、常识、政治、申论均带解析。
- 首页为总览 + 考情，随后是政治理论、常识、言语、数量、判断、资料、申论、税务专项、冲刺计划、考场清单十个章节页。

## 配置项

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADDR` | `:8080` | 监听地址 |
| `DB_PATH` | `data/study.db`（容器内 `/data/study.db`） | SQLite 文件路径，目录会自动创建 |
| `TZ` | `Asia/Shanghai` | 时区，影响倒计时“今天”的判定 |
| `APP_PORT` | `8080` | 仅 Compose 使用，宿主机映射端口 |

## API

页面本身是服务端渲染，下面两个 JSON 接口只负责保存状态：

```bash
# 勾选 / 取消勾选一个知识点
curl -X POST http://localhost:8080/api/progress \
  -H 'Content-Type: application/json' \
  -d '{"id":"p1","done":true}'
# => {"ok":true,"done":true,"done_count":1,"total":160,"percent":1}

# 修改考试日期
curl -X POST http://localhost:8080/api/exam-date \
  -H 'Content-Type: application/json' \
  -d '{"date":"2026-11-29"}'
# => {"ok":true,"exam_date":"2026-11-29","days_left":18}

# 提交一道练习题（单选传选项，申论题传 mastered / review 自评）
curl -X POST http://localhost:8080/api/practice/answer \
  -H 'Content-Type: application/json' \
  -d '{"question_id":"pol-01","choice":"A"}'
# => {"ok":true,"correct":true,"answer":"A","explanation":"...","stats":{...}}
```

`id` 必须是页面中存在的知识点或题目 ID；接口只接受 `application/json`，并校验同源 `Origin`。另有 `GET /healthz` 供容器健康检查。

## 数据备份

```bash
# 方式一：直接打包数据卷（建议先停容器）
docker compose stop app
docker run --rm -v gongkao-study-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/study-data.tgz -C /data .
docker compose start app

# 方式二：用 sqlite3 在线备份
docker compose exec app sh -c 'wget -qO- http://127.0.0.1:8080/healthz'  # 确认服务正常
# 宿主机装有 sqlite3 时也可直接备份挂载出来的文件
```

## 目录结构

```
study-app/
├── main.go                     # 入口：配置、启动、优雅退出
├── internal/
│   ├── store/store.go          # SQLite：建表、进度读写、设置读写
│   └── web/server.go           # 路由、模板渲染、JSON 接口、中间件
├── web/templates/index.html    # 服务端模板（含全部学习内容）
├── web/templates/practice.html # 章节练习页面
├── web/data/questions.json     # 题库（48 题，启动时写入 SQLite）
├── tools/build_template.py     # 由静态页生成模板的脚本（内容升级时用）
├── tools/build_questions.py    # 题库生成脚本（改题后重新生成 JSON）
├── Dockerfile                  # 多阶段构建，纯静态二进制
├── docker-compose.yml          # 单服务 + 持久化数据卷
└── data/                       # 本地运行时的数据库目录（已 gitignore）
```

## 内容说明

内容依据课程讲义与公开考情整理，时政部分截至 2025 年中；临近考试请自行补充最新时政。
练习题的 27 道真题转录自课程资料中的《2025 年国考行测（地市级）》，其余为按讲义考点编写的模拟题；
如发现文字差异，以原卷为准。本应用仅供个人备考复习使用。

如果部署到公网，建议在前面加一层反向代理并启用访问认证——应用本身按单人使用设计，没有账号体系。
