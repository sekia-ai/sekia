-- ai-issue-classifier.lua
-- Uses AI to classify new GitHub issues and apply the appropriate label.

sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.issue.opened" then return end

    local prompt = "Classify this GitHub issue. Reply with exactly one word: bug, feature, question, or docs.\n\n"
        .. "Title: " .. (event.payload.title or "") .. "\n"
        .. "Body: " .. (event.payload.body or "")

    local result, err = sekia.ai(prompt, {
        max_tokens = 16,
        temperature = 0,
    })

    if err then
        sekia.log("error", "AI classification failed: " .. err)
        return
    end

    local label = string.lower(string.gsub(result, "%s+", ""))
    if label ~= "bug" and label ~= "feature" and label ~= "question" and label ~= "docs" then
        sekia.log("warn", "AI returned unexpected label: " .. result)
        return
    end

    sekia.command("github-agent", "add_label", {
        owner  = event.payload.owner,
        repo   = event.payload.repo,
        number = event.payload.number,
        label  = label,
    })

    sekia.log("info", "AI classified issue #" .. event.payload.number .. " as: " .. label)
end)
