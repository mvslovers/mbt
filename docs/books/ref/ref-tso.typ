#import "../bookmaster/bookmaster.typ": *

= The TSO Test API <ref-tso>

#idx("mbt test", "--tso")
#cmd("mbt test --tso") runs each file #cmd("test/tso/*.lua") in a TSO
session of its own over TN3270, as the user of the target's
#cmd("[tn3270]"). The file returns a function, which is called with the
session #var("t"). The test passes when the function returns without an
error\; the session is logged off whatever happens. The Lua environment is
that of @ref-lua.

```
return function(t)
  t:logon()
  t:type("TIME"):enter()
  t:expect("TIME-")
  t:logoff()
end
```

#table(columns: (2.3in, 1fr),
  [Method], [Meaning],
  [#cmd("t:logon()")], [logs on with the target's TN3270 user and
    password.],
  [#cmd("t:logoff()")], [logs off.],
  [#cmd("t:type(")#var("s")#cmd(")")], [types #var("s") at the cursor.],
  [#cmd("t:enter()"), #cmd("t:clear()"), #cmd("t:tab()")], [press Enter,
    Clear, Tab.],
  [#cmd("t:pf(")#var("n")#cmd(")"), #cmd("t:pa(")#var("n")#cmd(")")], [press
    PF#var("n"), PA#var("n").],
  [#cmd("t:expect(")#var("text")#cmd(" [, { timeout = ")#var("s")#cmd(" }])")],
    [waits until #var("text") is on the screen, at most #var("s") seconds,
    default 10. Otherwise an error with the screen it saw.],
  [#cmd("t:screen()")], [the screen as a list of lines.],
  [#cmd("t:text()")], [the screen as one string, lines joined by newlines.],
  [#cmd("t:cursor()")], [the cursor's row and column.],
  [#cmd("t.testlib")], [the name of the test library of #cmd("mbt test
    --mvs").],
)

#cmd("type"), #cmd("enter"), #cmd("clear"), #cmd("tab"), #cmd("pf"),
#cmd("pa") and #cmd("expect") return the session, so calls can be chained:
#cmd("t:type(\"TIME\"):enter()").

A TSO user can be logged on only once. The tests run one after the other,
and the target's TN3270 user should not be one you are logged on with
yourself. #cmd("mbt test --tso") does not deploy the test modules\; run
#cmd("mbt test --mvs") first when a session calls one.
