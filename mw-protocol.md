# The Hollow Knight MultiWorld Protocol

The HK MultiWorld and ItemSync mods and server use a custom binary protocol to communicate.
This protocol was, until now, undocumented and the only reference was the
[MultiWorld source code][mwsrc] itself; this document aims to rectify that.

It covers both the details of messages are encoded, and the overall flow of
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

Sent both by the client as their initial message, and by the server as a response
to that. Contains one field:

- ServerName (string): a human-readable name for the server. Has no significance
  for the rest of the protocol.

When sent by the server, this message also has the SenderUID header field set to
the UID that has been assigned to the client.
