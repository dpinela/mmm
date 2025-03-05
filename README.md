Isthmus is a proxy server which allows an [Archipelago][] client to join a [MultiWorld][]
game, without modifications to either the client or the MultiWorld server.

[Archipelago]: https://archipelago.gg
[MultiWorld]: https://github.com/Shadudev/HollowKnight.MultiWorld/

# Installation

To install this tool, you can either download a binary release for your platform, or compile it from
source.

To do the latter, you will need a [Go][] toolchain and a C compiler. Once you have them, run this 
command:

    go install github.com/dpinela/mmm/cmd/isthmus@latest

[Go]: https://go.dev

# Usage

To use Isthmus, you will need to first generate a **solo** Archipelago seed with a local Archipelago
installation; see the [official guide][guide] for instructions on how to do this on Windows,
or the [source installation instructions][srcguide] for other operating systems.

Once generated, extract the .archipelago file from the archive that is placed in the `output`
directory, and launch Isthmus from the command line like this:

    isthmus -apfile /path/to/apfile.archipelago -mwroom eggu -savefile savefile.isthmus

If you don't have the `isthmus` executable in your PATH, you will need to navigate to the directory
where you put it first, and also invoke it as `./isthmus` if not on Windows. Make sure to replace
the path with the path to the actual .archipelago file you want to use, and "eggu" with the
MultiWorld room you want to join. Also keep in mind that the savefile should not already exist;
if it does, choose a different name or delete the existing file instead.

This will connect you to the main MW public server, and join you to the room you indicated. Now,
wait until one of the native MultiWorld players issues the "Start MW" command to shuffle the worlds,
at which point you will see these messages:

    2025/03/02 17:26:54 MW setup complete
    2025/03/02 17:26:54 Starting up AP server

You can now launch your game/Archipelago client and connect to Isthmus. To do that, set the
Archipelago server address to `localhost` and the port to `38281`, and start a new game.

To shut down Isthmus, press Control-C. If you later need to restart it, whether because you closed
it manually, because you restarted your computer, or in the event of a crash, simply re-run the
same command you used to start it the first time.

## Options

The `isthmus` command accepts these options; each should be followed by its argument on the command
line (quoted if it contains spaces):

- `-apfile`: The path to the .archipelago file.
- `-mwserver`: The MultiWorld server to use; defaults to the main MultiWorld public server at mw.hkmp.org.
- `-mwroom`: The room to connect to.
- `-apport`: The local port on which Isthmus will accept connections from your Archipelago client;
  defaults to 38281, the default port Archipelago normally uses.
- `-savefile`: The path to your savefile. This is used to store information about item placements
  after the MW shuffle and to record exchanged items during your game.

[guide]: https://archipelago.gg/tutorial/Archipelago/setup/en#archipelago-setup-guide
[srcguide]: https://github.com/ArchipelagoMW/Archipelago/blob/main/docs/running%20from%20source.md

# Compatibility

Isthmus should be compatibile with any Archipelago client that uses remote items
(that is, all collected items are sent by the server, including ones in its own world).
Clients that do not support remote items will not work correctly.

The following clients are known to be compatible:

- [Hollow Knight][hkap]
- [TUNIC][tunc]

[hkap]: https://github.com/ArchipelagoMW-HollowKnight/Archipelago.HollowKnight
[tunc]: https://github.com/silent-destroyer/tunic-randomizer/

## Feature limitations

Isthmus supports basic exchange of items between worlds; other features, like hinting or
DeathLink, that do not have equivalents in MultiWorld are not implemented.

It is only possible to connect one client at a time to Isthmus's server; to use tools like the
text client, you'll have to disconnect the game before using them.

The only [text commands][txt] supported are `!collect` and `!release`. Other commands will have
no effect.

[txt]: https://archipelago.gg/tutorial/Archipelago/commands/en