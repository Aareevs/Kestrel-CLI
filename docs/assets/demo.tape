# VHS tape for the README demo: https://github.com/charmbracelet/vhs
# Generate from the repo root with:  vhs docs/assets/demo.tape
# The GIF is not committed until it has been generated from a real run.
Output docs/assets/demo.gif
Set FontSize 15
Set Width 1100
Set Height 700
Set Theme "Catppuccin Mocha"

Type "go build -o /tmp/kestrel ./cmd/kestrel" Enter
Sleep 3s
Type "/tmp/kestrel inspect --project tests/fixtures/projects/compose-stack" Enter
Sleep 4s
Type "/tmp/kestrel init --print --project tests/fixtures/projects/compose-stack" Enter
Sleep 4s
