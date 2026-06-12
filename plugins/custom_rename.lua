-- Filerename 示例 Lua 插件
-- 在文件名后添加自定义前缀和后缀

local prefix = plugin.args.prefix or ""
local suffix = plugin.args.suffix or ""

function rename(file, index, current_name)
    local result = current_name

    if prefix ~= "" then
        result = prefix .. "_" .. result
    end

    if suffix ~= "" then
        result = result .. "_" .. suffix
    end

    if file.size > 10485760 then
        result = result .. "_large"
    end

    log("Processing: " .. file.path .. " -> " .. result)

    return result, true
end
