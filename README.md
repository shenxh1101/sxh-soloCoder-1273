# filerename - 高性能文件批量重命名与整理工具

用 Go 开发的高性能命令行文件批量重命名与整理工具。

## 功能特性

- 🔍 **智能扫描**：递归目录扫描，支持正则表达式、通配符匹配
- 🔢 **序列号重命名**：自定义起始值、步长、零填充宽度、位置（前缀/后缀）
- 📅 **日期时间提取**：EXIF 照片数据、文件创建时间、文件名时间戳自动解析
- 🔤 **大小写转换**：snake_case、kebab-case、camelCase、PascalCase 自动识别和转换
- 🧹 **智能去重**：同名文件自动添加后缀或移动到冲突文件夹
- 📂 **文件类型分类**：按扩展名自动移动到图片/视频/文档/代码等文件夹
- 👀 **预览模式**：执行前展示完整的 diff 对比
- ↩️ **撤销功能**：支持撤销最近 100 次操作
- 🔐 **内容哈希去重**：扫描磁盘找出完全相同的重复文件
- 🔌 **Lua 插件系统**：编写自定义重命名规则
- ⚡ **并行处理**：利用 goroutine 在 SSD 上达到每秒 10000+ 处理速度
- 📊 **进度条 + ETA**：可视化进度和估算完成时间
- ⏸️ **断点续传**：大任务支持中断恢复
- ⚙️ **TOML 配置**：全局配置 + 项目级配置继承

## 安装

```bash
git clone <repo>
cd filerename
go build -o filerename ./cmd/filerename
```

## 快速开始

```bash
# 预览（不执行）所有 jpg 文件按日期+序列号重命名
filerename ~/Photos --pattern "*.jpg" --date --date-source exif --seq --seq-width 4 -p

# 执行重命名
filerename ~/Photos --pattern "*.jpg" --date --seq

# 转换所有文件为 snake_case
filerename ./src --case --case-target snake_case

# 按文件类型自动分类
filerename ~/Downloads --classify

# 使用正则替换文件名
filerename ./docs --regex "IMG_(\d{8})_(\d{6})" --pattern "*IMG*" --regex-pattern "IMG_(\d{8})_(\d{6})" --date --date-source filename

# 查找重复文件
filerename dedup ~/Videos --min-size 1048576

# 查看操作历史
filerename history

# 撤销最近一次操作
filerename undo

# 生成示例配置
filerename config ./filerename.toml
```

## 配置示例（.filerename.toml）

```toml
[global]
workers = 32
recursive = true

[rename.datetime]
enabled = true
source = "exif"
format = "2006-01-02_150405"
position = "prefix"

[rename.sequence]
enabled = true
start = 1
step = 1
width = 4
position = "suffix"
```

## Lua 插件示例

创建 `plugins/myrule.lua`:

```lua
function rename(file, index, current_name)
    -- 在 helper 命名空间下可用: snake_case, camel_case, pascal_case, kebab_case, pad_left, pad_right
    local clean = helper.snake_case(current_name)
    local num = helper.pad_left(tostring(index + 1), 5, "0")
    return num .. "_" .. clean, true
end
```

## 命令速查

| 命令 | 说明 |
|------|------|
| `filerename [paths...]` | 重命名/整理文件 |
| `filerename dedup` | 查找并清理重复文件 |
| `filerename undo [id]` | 撤销操作 |
| `filerename history` | 查看操作历史 |
| `filerename list` | 列出匹配文件 |
| `filerename config` | 生成示例配置 |
