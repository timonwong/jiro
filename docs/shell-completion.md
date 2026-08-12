# Shell Completion

jiro uses Cobra's native completion support for Bash, Zsh, Fish, and
PowerShell. Generate and load a script for the current session with:

```sh
# Bash
source <(jiro completion bash)

# Zsh (requires compinit)
source <(jiro completion zsh)

# Fish
jiro completion fish | source

# PowerShell
jiro completion powershell | Out-String | Invoke-Expression
```

For normal use, generate the script once in the shell's completion directory
instead of running jiro during every shell startup:

```sh
mkdir -p ~/.zsh/completions
jiro completion zsh > ~/.zsh/completions/_jiro
# Add ~/.zsh/completions to fpath before running compinit in ~/.zshrc.

mkdir -p ~/.config/fish/completions
jiro completion fish > ~/.config/fish/completions/jiro.fish
```

Completion covers commands, flags, enum values, local input paths, and named
Profiles from the selected config file. It does not contact Jira or read
Credentials from the OS keyring.
