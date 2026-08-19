Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 700
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 24

Hide
Type "mkdir -p /tmp/wz-demo && go build -o /tmp/wz-demo/wavezd ./cmd/wavezd && go build -o /tmp/wz-demo/wavez ./cmd/wavez"
Enter
Sleep 15s
Type "rm -f /tmp/wz-demo/d.sock; /tmp/wz-demo/wavezd -socket /tmp/wz-demo/d.sock -dir /Users/kyleking/Developer/kyleking/wavez > /tmp/wz-demo/wavezd.log 2>&1 & disown"
Enter
Sleep 1s
Type "clear"
Enter
Show

Type "/tmp/wz-demo/wavez -socket /tmp/wz-demo/d.sock"
Enter
Sleep 1500ms

# Home starts empty; seed it with a couple of idle threads (no prompt, so
# no model call) so the list has real rows to show.
Type "n"
Sleep 300ms
Enter
Sleep 700ms
Type "n"
Sleep 300ms
Enter
Sleep 700ms
Type "n"
Sleep 300ms
Enter
Sleep 1s
Screenshot docs/img/home.png

# Open a thread and show the composer, without sending (no model call).
Enter
Sleep 1s
Tab
Sleep 200ms
Tab
Sleep 300ms
Type "i draft the release notes for the sync fix"
Sleep 700ms
Screenshot docs/img/thread.png
Escape
Sleep 300ms
Escape
Sleep 500ms

# Back to Home, then the schedule view.
Escape
Sleep 500ms
Type "s"
Sleep 1s
Screenshot docs/img/schedule.png
Escape
Sleep 500ms

# Diagnostics panel.
Type "D"
Sleep 1s
Screenshot docs/img/diagnostics.png
Escape
Sleep 500ms

# Help overlay.
Type "?"
Sleep 1s
Screenshot docs/img/help.png
Escape
Sleep 500ms

Type "q"
Sleep 1s

Hide
Type "pkill -f wz-demo/wavezd"
Enter
Sleep 500ms
Show
