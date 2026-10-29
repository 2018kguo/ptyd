# From the repo root: vhs docs/demo.tape
# Real ptyd groups and terminals; local fixtures, no model-provider calls.
Output docs/demo.gif
Set Shell bash
Set FontSize 18
Set Width 1400
Set Height 800
Set Padding 20
Set TypingSpeed 65ms
Set Framerate 15
Set Theme "Catppuccin Mocha"

Hide
Type "source docs/demo-setup.sh" Enter
Sleep 2s
Type "clear" Enter
Show
Type "ptyd ls" Enter
Sleep 1800ms
Type "j"
Sleep 1400ms
Type "k"
Sleep 1400ms
Type "q"
Sleep 600ms
Type "ptyd attach website" Enter
Sleep 1800ms
Type "v"
Sleep 900ms
Type "j"
Sleep 900ms
Type "v"
Sleep 2500ms
Type "k"
Sleep 600ms
Enter
Sleep 1200ms
Type "work keeps running" Enter
Sleep 1200ms
Type@40ms "jk"
Sleep 1200ms
Type@40ms "jk"
Sleep 1200ms
Type@40ms "jk"
Sleep 800ms
Type "ptyd ls --plain" Enter
Sleep 1500ms

Hide
Type "exit" Enter
