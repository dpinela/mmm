

	CREATE TABLE IF NOT EXISTS mw_devoured_archipelagos (
		-- one archi per room simplifies things
		id INTEGER PRIMARY KEY,
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id) ON DELETE CASCADE,
		file BLOB NOT NULL
	);

	CREATE TABLE IF NOT EXISTS ap_slots (
		ap_id INTEGER NOT NULL REFERENCES mw_devoured_archipelagos (id) ON DELETE CASCADE,
		player_id INTEGER NOT NULL,
		game TEXT NOT NULL,
		slot_data BLOB NOT NULL,

		PRIMARY KEY (ap_id, player_id)
	);

	CREATE TABLE IF NOT EXISTS ap_player_placements (
		rando_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		sphere INTEGER NOT NULL,
		location_id INTEGER NOT NULL,
		item_id INTEGER NOT NULL,

		PRIMARY KEY (rando_id, player_id, location_id),

		FOREIGN KEY (rando_id, player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS ap_datapackages (
		ap_id INTEGER NOT NULL REFERENCES mw_devoured_archipelagos (id) ON DELETE CASCADE,
		game TEXT NOT NULL,
		datapackage BLOB NOT NULL,

		PRIMARY KEY (ap_id, game)
	);

	CREATE TABLE IF NOT EXISTS ap_data_storage (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id) ON DELETE CASCADE,
		key TEXT NOT NULL,
		json_value TEXT NOT NULL,

		PRIMARY KEY (rando_id, key)
	);

	CREATE TABLE IF NOT EXISTS ap_locations_cleared (
		rando_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		location_id INTEGER NOT NULL,

		PRIMARY KEY (rando_id, player_id, location_id),

		FOREIGN KEY (rando_id, player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS ap_sent_items (
		rando_id INTEGER NOT NULL,
		sender_id INTEGER NOT NULL,
		destination_player_id INTEGER NOT NULL,
		item_index INTEGER NOT NULL,
		item_id INTEGER NOT NULL,
		location_id INTEGER NOT NULL,
		flags INTEGER NOT NULL,

		PRIMARY KEY (rando_id, sender_id, destination_player_id, item_index),

		FOREIGN KEY (rando_id, sender_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,
		FOREIGN KEY (rando_id, destination_player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE
	);