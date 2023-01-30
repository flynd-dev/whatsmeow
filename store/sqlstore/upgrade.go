// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package sqlstore

import (
	"database/sql"
	"fmt"
)

type upgradeFunc func(*sql.Tx, *Container) error

// Upgrades is a list of functions that will upgrade a database to the latest version.
//
// This may be of use if you want to manage the database fully manually, but in most cases you
// should just call Container.Upgrade to let the library handle everything.
var Upgrades = [...]upgradeFunc{upgradeV1, upgradeV2, upgradeV3, upgradeV4}

func (c *Container) getVersion() (int, error) {
	_, err := c.db.Exec("CREATE TABLE IF NOT EXISTS whatsmeow_version (version INTEGER)")
	if err != nil {
		return -1, err
	}

	version := 0
	row := c.db.QueryRow("SELECT version FROM whatsmeow_version LIMIT 1")
	if row != nil {
		_ = row.Scan(&version)
	}
	return version, nil
}

func (c *Container) setVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec("DELETE FROM whatsmeow_version")
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO whatsmeow_version (version) VALUES (?)", version)
	return err
}

// Upgrade upgrades the database from the current to the latest version available.
func (c *Container) Upgrade() error {
	version, err := c.getVersion()
	if err != nil {
		return err
	}

	for ; version < len(Upgrades); version++ {
		var tx *sql.Tx
		tx, err = c.db.Begin()
		if err != nil {
			return err
		}

		migrateFunc := Upgrades[version]
		c.log.Infof("Upgrading database to v%d", version+1)
		err = migrateFunc(tx, c)
		if err != nil {
			_ = tx.Rollback()
			return err
		}

		if err = c.setVersion(tx, version+1); err != nil {
			return err
		}

		if err = tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func upgradeV1(tx *sql.Tx, _ *Container) error {
	_, err := tx.Exec(`CREATE TABLE whatsmeow_device (
		jid VARCHAR(255) ,

		registration_id BIGINT NOT NULL CHECK ( registration_id >= 0 AND registration_id < 4294967296 ),

		noise_key    LONGBLOB NOT NULL CHECK ( length(noise_key) = 32 ),
		identity_key LONGBLOB NOT NULL CHECK ( length(identity_key) = 32 ),

		signed_pre_key     LONGBLOB   NOT NULL CHECK ( length(signed_pre_key) = 32 ),
		signed_pre_key_id  INTEGER NOT NULL CHECK ( signed_pre_key_id >= 0 AND signed_pre_key_id < 16777216 ),
		signed_pre_key_sig LONGBLOB   NOT NULL CHECK ( length(signed_pre_key_sig) = 64 ),

		adv_key         LONGBLOB NOT NULL,
		adv_details     LONGBLOB NOT NULL,
		adv_account_sig LONGBLOB NOT NULL CHECK ( length(adv_account_sig) = 64 ),
		adv_device_sig  LONGBLOB NOT NULL CHECK ( length(adv_device_sig) = 64 ),

		platform      TEXT NOT NULL,
		business_name TEXT NOT NULL,
		push_name     TEXT NOT NULL,
		PRIMARY KEY (jid)
);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_identity_keys (
		our_jid  VARCHAR(255),
		their_id TEXT,
		identity LONGBLOB NOT NULL CHECK ( length(identity) = 32 ),

		PRIMARY KEY (our_jid, their_id(20)),
		-- FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
        CONSTRAINT fk_wm_identity_keys
			FOREIGN KEY (our_jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(fmt.Sprintf(`CREATE TABLE whatsmeow_pre_keys (
		jid      VARCHAR(255),
		key_id   INTEGER   CHECK ( key_id >= 0 AND key_id < 16777216 ),
		%s   LONGBLOB  NOT NULL CHECK ( length(%s) = 32 ),
		uploaded BOOLEAN NOT NULL,

		PRIMARY KEY (jid, key_id),
		-- FOREIGN KEY (jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_pre_keys
			FOREIGN KEY (jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
		);`, "`key`", "`key`"))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_sessions (
		our_jid  VARCHAR(255),
		their_id TEXT,
		session  LONGBLOB,

		PRIMARY KEY (our_jid, their_id(20)),
		-- FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		
		CONSTRAINT fk_wm_sessions
			FOREIGN KEY (our_jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_sender_keys (
		our_jid    VARCHAR(255),
		chat_id    TEXT,
		sender_id  TEXT,
		sender_key LONGBLOB NOT NULL,

		PRIMARY KEY (our_jid, chat_id(20), sender_id(20)),
		-- FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		
		CONSTRAINT fk_wm_sender_keys
			FOREIGN KEY (our_jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_app_state_sync_keys (
		jid         VARCHAR(255),
		key_id      LONGBLOB,
		key_data    LONGBLOB  NOT NULL,
		timestamp   BIGINT NOT NULL,
		fingerprint LONGBLOB  NOT NULL,

		PRIMARY KEY (jid, key_id(20)),
		-- FOREIGN KEY (jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_app_state_sync_keys
			FOREIGN KEY (jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_app_state_version (
		jid     VARCHAR(255),
		name    VARCHAR(255),
		version BIGINT NOT NULL,
		hash    LONGBLOB  NOT NULL CHECK ( length(hash) = 128 ),

		PRIMARY KEY (jid, name),
		-- FOREIGN KEY (jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_app_state_version
			FOREIGN KEY (jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_app_state_mutation_macs (
		jid       VARCHAR(255),
		name      VARCHAR(255),
		version   BIGINT,
		index_mac LONGBLOB          CHECK ( length(index_mac) = 32 ),
		value_mac LONGBLOB NOT NULL CHECK ( length(value_mac) = 32 ),

		PRIMARY KEY (jid, name(20), version, index_mac(20)),
		-- FOREIGN KEY (jid, name) REFERENCES whatsmeow_app_state_version(jid, name) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_app_state_mutation_macs
			FOREIGN KEY (jid, name)
			REFERENCES whatsmeow_app_state_version (jid, name)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_contacts (
		our_jid       VARCHAR(255),
		their_jid     TEXT,
		first_name    TEXT,
		full_name     TEXT,
		push_name     TEXT,
		business_name TEXT,

		PRIMARY KEY (our_jid, their_jid(20)),
		-- FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_contacts
			FOREIGN KEY (our_jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE whatsmeow_chat_settings (
		our_jid       VARCHAR(255),
		chat_jid      TEXT,
		muted_until   BIGINT  NOT NULL DEFAULT 0,
		pinned        BOOLEAN NOT NULL DEFAULT false,
		archived      BOOLEAN NOT NULL DEFAULT false,

		PRIMARY KEY (our_jid, chat_jid(20)),
		-- FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
		CONSTRAINT fk_wm_chat_settings
			FOREIGN KEY (our_jid)
			REFERENCES whatsmeow_device (jid)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);`)
	if err != nil {
		return err
	}
	return nil
}

const fillSigKeyPostgres = `
UPDATE whatsmeow_device SET adv_account_sig_key=(
	SELECT identity
	FROM whatsmeow_identity_keys
	WHERE our_jid=whatsmeow_device.jid
	  AND their_id=concat(split_part(whatsmeow_device.jid, '.', 1), '0')
);
DELETE FROM whatsmeow_device WHERE adv_account_sig_key IS NULL;
ALTER TABLE whatsmeow_device ALTER COLUMN adv_account_sig_key SET NOT NULL;
`

const fillSigKeySQLite = `
UPDATE whatsmeow_device SET adv_account_sig_key=(
	SELECT identity
	FROM whatsmeow_identity_keys
	WHERE our_jid=whatsmeow_device.jid
	  AND their_id=substr(whatsmeow_device.jid, 0, instr(whatsmeow_device.jid, '.')) || '0'
)
`

func upgradeV2(tx *sql.Tx, container *Container) error {
	_, err := tx.Exec("ALTER TABLE whatsmeow_device ADD COLUMN adv_account_sig_key LONGBLOB CHECK ( length(adv_account_sig_key) = 32 )")
	if err != nil {
		return err
	}
	if container.dialect == "postgres" || container.dialect == "pgx" {
		_, err = tx.Exec(fillSigKeyPostgres)
	} else {
		_, err = tx.Exec(fillSigKeySQLite)
	}
	return err
}

func upgradeV3(tx *sql.Tx, container *Container) error {
	_, err := tx.Exec(fmt.Sprintf(`
	CREATE TABLE whatsmeow_message_secrets (
	  our_jid VARCHAR(255) NULL DEFAULT NULL,
	  chat_jid TEXT NULL DEFAULT NULL,
	  sender_jid TEXT NULL DEFAULT NULL,
	  message_id TEXT NULL DEFAULT NULL,
	  %s LONGBLOB NOT NULL CHECK ( length(%s) = 64 ),
  
  	CONSTRAINT fk_wm_msg_scrt
		FOREIGN KEY (our_jid)
		REFERENCES whatsmeow_device (jid)
		ON DELETE CASCADE
		ON UPDATE CASCADE);
	`, "`key`", "`key`"))
	return err
}

func upgradeV4(tx *sql.Tx, container *Container) error {
	_, err := tx.Exec(`CREATE TABLE whatsmeow_privacy_tokens (
		our_jid   TEXT,
		their_jid TEXT,
		token     bytea  NOT NULL,
		timestamp BIGINT NOT NULL,
		PRIMARY KEY (our_jid, their_jid)
	)`)
	return err
}
