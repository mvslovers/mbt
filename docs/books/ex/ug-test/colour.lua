-- test/tso/colour.lua
return function(t)
  t:logon()
  t:type("TIME"):enter()
  t:expect("TIME-")
  local row
  for r, line in ipairs(t:screen()) do
    if line:find("TIME-", 1, true) then row = r end
  end
  assert(t:color(row, 2) == "red", "the answer is not red")
  assert(t:color(row - 1, 1) == "green", "the command is not green")
  t:logoff()
end
