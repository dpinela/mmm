# The Hollow Knight MultiWorld Protocol

The HK MultiWorld and ItemSync mods and server use a custom binary protocol over
TCP to communicate.
This protocol was, until now, undocumented and the only reference was the
[MultiWorld source code][mwsrc] itself; this document aims to rectify that.

It covers both the details of how messages are encoded, and the overall flow of
messages during operation.

[mwsrc]: https://github.com/Shadudev/HollowKnight.MultiWorld/

## Message flows

### Connecting and setting up a game

- The client sends a Connect message to the server, with all fields blank.
- The server responds with a Connect message, whose SenderUid is actually the UID assigned to the client.
- The client sends a Ready message indicating the room, nickname and mode to use.
- The server sends a ReadyConfirm message to all connected players in that room,
  including the client that just joined, listing the names of those players.
  It may send more such messages as new players join.
- One of the clients sends an InitiateGame message.
- The server sends a RequestRando message to all connected players in the room.
- The client responds to that with a RandoGenerated message listing their item
  placements and their hash.
- Once it has received the item placements from all players in the room,
  the server sends a Result message to each player, containing placements for
  all players and the ID of the rando, which can be used to join the game.

## Message encoding

### General considerations

Fixed-width integers are encoded in little-endian format.

Strings are encoded as a length prefix followed by the UTF-8 encoding of the
string.

String lengths are sent in little-endian order, seven bits at the time;
the high bit of each byte indicates whether more bytes follow, and the remaining
bits are the next highest seven bits of the value.

(This is how .NET's [BinaryWriter][] writes strings by default; see the [Write7BitEncodedInt][] method in particular.)

[BinaryWriter]: https://learn.microsoft.com/en-us/dotnet/api/system.io.binarywriter
[Write7BitEncodedInt]: https://learn.microsoft.com/en-us/dotnet/api/system.io.binarywriter.write7bitencodedint

### Header

The header is included at the start of all messages, and contains the following
fields:

- Length (32-bit unsigned integer): the total size of the message, including the
  length field itself.
- Type (32-bit unsigned integer): specifies the kind of content that follows the header.
- SenderUID (64-bit unsigned integer): the UID of the client that sent the
  message. For server messages this is normally 0, except for the Connect message.
- MessageID (64-bit unsigned integer): unused.

### Connect (Type 2)

Sent both by the client as its initial message, and by the server as a response
to that. Contains one field:

- ServerName (string): a human-readable name for the server. Has no significance
  for the rest of the protocol.

When sent by the server, this message also has the SenderUID header field set to
the UID that has been assigned to the client.

### Disconnect (Type 3)

Sent by the client to signal an intent to disconnect from the server. Contains
no fields.

### Ping (Type 12)

Sent regularly by the client to verify that the connection is still alive.
Contains one unused field:

- ReplyValue (32-bit unsigned integer)

### Ready (Type 13)

Sent by the client to join a room. Contains the following:

- Room (string): the name of the room to join.
- Nickname (string): the nickname to use in that room.
  This will be displayed in-game once the game is joined, too.
- ReadyMode (8-bit integer): always 0. Formerly differentiated between MultiWorld (0)
  and ItemSync (1); now the latter has its own Ready message type.
- ReadyMetadata (JSON string): an array of [key, value] arrays which will be
  broadcast to other players with the Result message.

### Ready Confirm (Type 10)

Sent by the server to notify the client that it has joined a room and to indicate which
other players are connected. Contains two fields:

- Ready (32-bit unsigned integer): the number of players in the room (redundant with Names)
- Names (JSON string): an array containing the Nicknames of every player in the room

### Ready Deny (Type 11)

Sent by the server to indicate that the client cannot join a room. Contains one field:

- Description (string): a human-readable explanation of why the join failed.

### Rando Generated (Type 16)

Sent by the client in response to a Request Rando; contains two fields:

- Items (JSON string): an object mapping group names to arrays of Placement objects.
  A Placement object has two keys:
    - Item1 (string): the name of the item placed.
    - Item2 (string): the location of that item.
- Seed (32-bit signed integer): a seed to be used by the server's randomization algorithm.

### Unready (Type 17)

Sent by the client to leave its current room. Contains no fields.

### Initiate Game (Type 18)

Sent by the client to start the process of mixing together everyone's seeds. Contains
one field:

- Settings (JSON string): an object with two keys:
  - Seed: an unused integer.
  - RandomizationAlgorithm: can only be 0; other values reserved for future use.

### Request Rando (Type 19)

Sent by the server to all players in a room after one of them sends an Initiate Game,
to request each of them to send their local seed. Contains no fields.