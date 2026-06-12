-- 根据文件大小自动分类命名的 Lua 插件示例

function rename(file, index, current_name)
    local size_mb = file.size / (1024 * 1024)
    local category = ""

    if size_mb < 1 then
        category = "tiny"
    elseif size_mb < 10 then
        category = "small"
    elseif size_mb < 100 then
        category = "medium"
    elseif size_mb < 1024 then
        category = "large"
    else
        category = "huge"
    end

    local seq = helper.pad_left(tostring(index + 1), 4, "0")
    local result = category .. "_" .. seq .. "_" .. helper.snake_case(current_name)

    return result, true
end
