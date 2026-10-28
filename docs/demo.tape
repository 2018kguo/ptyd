# From the repo root: vhs docs/demo.tape
# Records real ptyd terminals with local fixtures; no agent login or provider.
Output docs/demo.gif
Set Shell bash
Set FontSize 18
Set Width 1200
Set Height 760
Set Padding 20
Set TypingSpeed 65ms
Set Framerate 15
Set Theme "Catppuccin Mocha"

Hide
Type "source docs/demo-setup.sh" Enter
Sleep 2s
Type "clear" Enter
Show
Type "ptyd ui --discover=false" Enter
Sleep 1200ms
Type "?"
Sleep 3s
Type "q"
Type ":b build" Enter
Sleep 1500ms
Type "hello from the keyboard" Enter
Sleep 1500ms
Type@40ms "jk"
Sleep 1200ms
Type "/logs" Enter
Sleep 1200ms
Type "i"
Sleep 1500ms
Type "work continues while you switch" Enter
Sleep 1200ms
Type@40ms "jk"
Sleep 1200ms
Type ":ls" Enter
Sleep 1200ms
Type ":q" Enter
Sleep 1500ms

Hide
Type "exit" Enter
