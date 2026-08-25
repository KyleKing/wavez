Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1000
Set Height 600
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
Sleep 5s

Type "j"
Sleep 600ms
Type "j"
Sleep 1s
Screenshot docs/img/home.png

Type "v"
Sleep 1s
Screenshot docs/img/home-goal.png
Type "v"
Sleep 600ms

Enter
Sleep 2s
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

Escape
Sleep 500ms
Type "s"
Sleep 1s
Screenshot docs/img/schedule.png
Escape
Sleep 500ms

Type "D"
Sleep 1s
Screenshot docs/img/diagnostics.png
Escape
Sleep 500ms

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
