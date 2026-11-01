gocord Mac kit @VERSION@ (Apple Silicon only)

1. Copy the Discord bot token to the clipboard. On a Mac that already has it:
     security find-generic-password -s discord-mcp -a DISCORD_BOT_TOKEN -w | pbcopy
   (Skip this if the token is already in this Mac's Keychain.)

2. In Terminal, from this folder:
     bash install.sh

   It stores the token in the Keychain and clears the clipboard, installs the
   server to ~/.local, registers the Claude Code plugin, and opens the Claude
   Desktop extension installer.

3. In Claude Desktop, click Install and leave the token field empty.

4. Cowork: if "discord" isn't already under Customize > Plugins, upload
   discord.plugin (install.sh shows it in Finder).

Options: --no-code (skip Claude Code), --no-desktop (skip Desktop/Cowork).
Re-running install.sh upgrades in place. The kit contains no token.

Only one Mac should run Claude with this bot at a time: Discord approval
buttons may be answered by whichever machine receives them first.
