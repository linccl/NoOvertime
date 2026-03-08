package api

import "github.com/jackc/pgx/v5/pgconn"

const testTargetTokenUserID = "1c9b1b1c-93cb-4c85-bf1a-245f0d4a8f1e"
const testTargetDeviceID = "f2df11ef-7240-42b2-8ceb-623ad7711e0c"

func newMigrationTestMobileTokens() map[string]fakeMobileTokenRecord {
	return cloneFakeMobileTokenRecords(map[string]fakeMobileTokenRecord{
		hashMobileToken(testSyncToken): {
			UserID:      testUserID,
			DeviceID:    testDeviceID,
			WriterEpoch: testWriterEpoch,
			Status:      mobileTokenStateActive,
		},
		hashMobileToken(testTargetDeviceToken): {
			UserID:      testTargetTokenUserID,
			DeviceID:    testTargetDeviceID,
			WriterEpoch: testWriterEpoch,
			Status:      mobileTokenStateActive,
		},
	})
}

func rotateMigrationTestActiveMobileTokensByUser(tokens map[string]fakeMobileTokenRecord, userID string) {
	for tokenHash, record := range tokens {
		if record.UserID != userID || record.Status != mobileTokenStateActive {
			continue
		}
		record.Status = mobileTokenStateRotated
		tokens[tokenHash] = record
	}
}

func bindMigrationTestMobileTokensByDevice(
	tokens map[string]fakeMobileTokenRecord,
	deviceID, userID string,
	writerEpoch int64,
) error {
	for tokenHash, record := range tokens {
		if record.DeviceID != deviceID || record.Status != mobileTokenStateActive {
			continue
		}
		if userID != "" && hasOtherMigrationTestActiveMobileToken(tokens, userID, tokenHash) {
			return &pgconn.PgError{Code: "23505", ConstraintName: "uq_mobile_tokens_active_user"}
		}
		record.UserID = userID
		record.WriterEpoch = writerEpoch
		tokens[tokenHash] = record
	}
	return nil
}

func hasOtherMigrationTestActiveMobileToken(
	tokens map[string]fakeMobileTokenRecord,
	userID, excludeTokenHash string,
) bool {
	for tokenHash, record := range tokens {
		if tokenHash == excludeTokenHash {
			continue
		}
		if record.UserID == userID && record.Status == mobileTokenStateActive {
			return true
		}
	}
	return false
}
