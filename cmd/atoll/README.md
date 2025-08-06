Atoll is a [MultiWorld][] server implementation designed to support asynchronous multiworlds -
that is, games where not all players need to be connected at the same time, whether during the
setup phase or during gameplay.

To this end, it offers three features over the standard MultiWorld server:

- Async setup - players can join a room and then exit their game/client without losing the world
  they submitted.
- Persisent storage for all games hosted on the server, including submitted worlds, shuffled
  placements, and sent items. This makes it safe to stop and restart the server at any time, even
  with some games ongoing.
- A web-based management console for creating multiworld games, managing connected players and
  triggering the shuffle even if no players are connected.

It is compatible with all existing MultiWorld clients, including Isthmus.

[MultiWorld]: https://github.com/Shadudev/HollowKnight.MultiWorld/

# Installation

Atoll is provided as source code only; to compile it, you will need a [Go][] and a C compiler.
Once you have them, clone or download this repository, then run the following at the root:

    go install ./cmd/atoll

# Usage

To run Atoll, you will need a configuration file named `config.json`. You can find a sample
configuration in this directory, in the `sample-config.json` file.

We recommend placing this file in a dedicated directory for Atoll, as it will create additional
files during operation.

Having written this file, start the server by running

    atoll /path/to/configdir

where the path is to the directory where you placed the `config.json` file (not the file itself).

## Configuration settings

- ListenAddress: the IP address on which the server will listen for connections. This should
  usually be set to `""` to listen for all inbound connections, or `"localhost"` if you are
  setting up a purely local server.
- ConsolePort: the TCP port on which the management console will be served.
- MWPort: the TCP port on which game clients should connect to the server.

## Setting up a multiworld

Access `http://localhost:8000` (presuming the sample settings) in a browser, then press the
"Create Room" button. This will bring you to a page displaying the room name, a Shuffle button,
and a list of players (which at this point will be empty).

To let other people join, give them the room name and the host name/IP address of the server
(the port is not necessary if you stick to the default, which is the same as the standard server).

As people join (see below), you can reload the page to see their nicknames. Alongside each,
you should see a tick mark indicating their world has been submitted, and a checkbox. Selecting
it allows you access to a Delete button, which deletes that world from the room.

Once you're done gathering worlds and are ready to start the game, press the Shuffle button. After
this, it will no longer be possible to add or remove players, and the existing ones can start
playing.

## Joining a multiworld

- Configure your local randomizer as desired, including any desired connections.
- **Save your seed and settings** - this part is important! For Hollow Knight players, the easiest way is to generate a code with [RandoSettingsManager][] and save it.
- Connect to the multiworld server, using the server address and room name you got from the person
  organizing the game.
- Make sure to **save your nickname** - you will need it later too.
- Once you're readied, your seed has been saved - you can disconnect and close the game now!

[RandoSettingsManager]: https://github.com/BadMagic100/RandoSettingsManager/

## How to start playing

- Set the same exact seed and settings in your randomizer as you did when joining.
  (This is where you use the RandoSettingsManager code, if you made one)
- Connect to the server, using the same server address, room name and nickname as before.
- You should see a short hexadecimal string displayed.
    - If you see "ERROR: seed does not match original", your seed and settings don't match what
      you previously used when joining; double-check those and also your nickname.
- Join the game as you would for any other multiworld.

