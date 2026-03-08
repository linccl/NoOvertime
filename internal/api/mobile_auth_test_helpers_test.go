package api

const testTargetDeviceToken = "tok_target_bound"

func testMobileAuthContextForToken(token string) mobileAuthContext {
	switch token {
	case testAnonymousSyncToken:
		return mobileAuthContext{
			Token:       token,
			DeviceID:    testDeviceID,
			WriterEpoch: 1,
			TokenStatus: mobileTokenAnonymous,
		}
	case testTargetDeviceToken:
		return mobileAuthContext{
			Token:       token,
			UserID:      testTargetTokenUserID,
			DeviceID:    testTargetDeviceID,
			WriterEpoch: testWriterEpoch,
			TokenStatus: mobileTokenBound,
		}
	default:
		return mobileAuthContext{
			Token:       token,
			UserID:      testUserID,
			DeviceID:    testDeviceID,
			WriterEpoch: testWriterEpoch,
			TokenStatus: mobileTokenBound,
		}
	}
}
