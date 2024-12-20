package uptime_backend

import (
	logging "github.com/ipfs/go-log/v2"
	sheets "google.golang.org/api/sheets/v4"
)

// Process rows retrieved from Google spreadsheet
// and extract public keys from the first column.
func processRows(rows [][](interface{})) Whitelist {
	wl := make(Whitelist)
	for _, row := range rows {
		if len(row) > 0 {
			switch v := row[0].(type) {
			case string:
				var pk Pk
				err := StringToPk(&pk, v)
				if err == nil {
					wl[pk] = struct{}{}
				}
			}
		}
	}
	return wl
}

// Retrieve data from delegation program spreadsheet
// and extract public keys out of the column containing
// public keys of program participants.
func RetrieveWhitelist(service *sheets.Service, log logging.StandardLogger, gSheetId string) Whitelist {
	col := DELEGATION_WHITELIST_COLUMN
	readRange := DELEGATION_WHITELIST_LIST + "!" + col + ":" + col
	resp, err := service.Spreadsheets.Values.Get(gSheetId, readRange).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve data from sheet: %v", err)
	}
	return processRows(resp.Values)
}
