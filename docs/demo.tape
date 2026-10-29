# From the repo root: vhs docs/demo.tape
# Real ptyd groups and terminals; local fixtures, no model-provider calls.
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1920
Set Height 1000
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
Type "/Requests served" Enter
Sleep 1500ms
Type ":ls" Enter
Sleep 900ms
Type "q"
Sleep 600ms
Type "ptyd attach website" Enter
Sleep 1800ms
Type "n"
Sleep 900ms
Type "f"
Sleep 600ms
Type "f"
Sleep 600ms
Type@40ms "jk"
Sleep 500ms
Type@100ms "vjvjvjvjvjvjvj"
Sleep 2500ms
Type "0"
Sleep 600ms
Enter
Sleep 1200ms
Type "work keeps running" Enter
Sleep 1200ms
Type "exit" Enter
Sleep 1500ms
Type "R"
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
