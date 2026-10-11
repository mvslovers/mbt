#import "../bookmaster/bookmaster.typ": *

= The TSO Test API <ref-tso>

#idx("mbt test", "--tso")
#cmd("mbt test --tso") runs each file #cmd("test/tso/*.lua") in a TSO
session of its own over TN3270, as the user of the target's
#cmd("[tn3270]")\; a target without one is an error (return code 2). The
test is named after its file in upper case\; #cmd("--only") #var("NAME"),
repeatable, in any case, runs a subset. The hooks and tasks of
#cmd("before_test") run first, those of #cmd("after_test") afterwards\; the
hooks of both see #cmd("ctx.kind") #cmd("\"tso\"").

The file must return a function, which is called with the session
#var("t"). The test passes when the function returns without an error.
The Lua environment is that of @ref-lua.

```
return function(t)
  t:logon()
  t:type("TIME"):enter()
  t:expect("TIME-")
  t:logoff()
end
```

== The Session <ref-tso-session>

#table(columns: (2.3in, 1fr),
  [Member], [Meaning],
  [#cmd("t:logon()")], [logs on with the target's TN3270 user, in upper
    case, and password, and waits for #cmd("READY"), at most two minutes.],
  [#cmd("t:logoff()")], [presses Clear, types #cmd("LOGOFF"), presses
    Enter and waits up to 30 seconds for #cmd("LOGGED OFF"). It does not
    leave ISPF first.],
  [#cmd("t:type(")#var("s")#cmd(")")], [types #var("s") at the cursor. An
    error, with the screen, when the keyboard is locked or the cursor is in
    a protected field.],
  [#cmd("t:enter()"), #cmd("t:clear()")], [press Enter, Clear.],
  [#cmd("t:pf(")#var("n")#cmd(")"), #cmd("t:pa(")#var("n")#cmd(")")], [press
    PF1 to PF24, PA1 to PA3. Another number is an error.],
  [#cmd("t:tab()")], [moves the cursor to the next input field\; sends
    nothing.],
  [#cmd("t:expect(")#var("text")#cmd(" [, { timeout = ")#var("s")#cmd(" }])")],
    [waits until #var("text"), case-sensitive, is on the screen, or was on
    it since the last key that went to the host. #var("s") is whole
    seconds, default 10. Otherwise an error with the 24 lines of the
    screen.],
  [#cmd("t:screen()")], [the screen: 24 lines, trailing blanks removed.
    Fields that are not displayed, such as a password, are blank.],
  [#cmd("t:text()")], [the same as one string, lines joined by newlines.],
  [#cmd("t:cursor()")], [the cursor's row and column, counted from 1.],
  [#cmd("t.user")], [the TN3270 user.],
  [#cmd("t.testlib")], [the name of the test library of #cmd("mbt test
    --mvs").],
  [#cmd("t.fixtures")], [the data sets of #cmd("[tso.NAME] fixtures"):
    #var("KEY")#cmd(" = ")#var("name") (@ref-tso-fixtures). Empty for a
    test without any.],
)

All methods except #cmd("screen"), #cmd("text") and #cmd("cursor") return
the session, so calls can be chained: #cmd("t:type(\"TIME\"):enter()").
Enter, Clear, PF and PA wait for the keyboard to be unlocked, at most 60
seconds, and then for 300 milliseconds without output.

== Fixtures <ref-tso-fixtures>

#idx("mbt test", "--tso fixtures")
A test that works on data sets of its own -- an exec, a CLIST, a panel,
input -- declares them in #cmd("mbt.toml"):

```
[tso.FSSMENU]
fixtures = { TSOTEST = ["test/tso/fssmenu.rexx"],
             RXLIB   = ["rxlib/FSSMENU.rexx", "rxlib/FSSAPI.rexx"] }
```

Before the sessions, and after the #cmd("before_test") hooks, MBT deletes
and allocates one PDS per key, #var("HLQ")#cmd(".")#var("PROJECT")#cmd(".FIX.")#var("TEST")#cmd(".")#var("KEY"),
FB 80, and loads the files into it with one job, #cmd("MBTTSOFX"), whose
number it prints. Member names and the load are those of the
#cmd("mbt test --mvs") fixtures (@ref-toml-test): each line becomes a
record, columns 73 to 80 included, nothing is numbered. A member that did not load fails the command
with return code 5 before any session. Only the tests selected with
#cmd("--only") are loaded. The test allocates what it needs itself:

```
t:type("ALLOC F(RXLIB) DA('" .. t.fixtures.RXLIB .. "') SHR"):enter()
```

== Logging Off <ref-tso-logoff>

When the function ends, after a successful #cmd("logon") and without a
#cmd("logoff") of its own, MBT logs the session off, whether the test
passed or failed. When that fails, the test fails, even if it had passed,
and MBT says so beside the test's own error: the user is most likely still
logged on, and every later logon fails until it is cancelled (operator
command #cmd("C U=")#var("user")). The tests after it are therefore not
run and are shown as #cmd("not run").

A TSO user can be logged on only once. The tests run one after the other,
and the target's TN3270 user should not be one you are logged on with
yourself. #cmd("mbt test --tso") does not deploy the test modules\; run
#cmd("mbt test --mvs") first when a session calls one. #cmd("-v") traces
the 3270 data stream, never a password.
