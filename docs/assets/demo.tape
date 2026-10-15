# Renders docs/assets/demo.gif from a real session: https://github.com/charmbracelet/vhs
#
#   make build && go build -o docs/assets/demo/bin/fakesvc ./tests/fakesvc
#   vhs docs/assets/demo.tape
#
# Nothing in it is staged: Kestrel supervises the demo stack, records what
# happens, and diagnoses it.

Output docs/assets/demo.gif
Set FontSize 15
Set Width 1200
Set Height 760
Set TypingSpeed 40ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PATH=$PWD/bin:$PATH KESTREL_HOME=$(mktemp -d) && cd docs/assets/demo && clear"
Enter
Show

Type "kestrel watch"
Sleep 300ms
Enter
Sleep 4s
Ctrl+C
Sleep 1500ms

Type "kestrel diagnose"
Sleep 300ms
Enter
Sleep 4s

Type "kestrel timeline"
Sleep 300ms
Enter
Sleep 4s
