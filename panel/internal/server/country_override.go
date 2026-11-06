package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ISO 3166-1 alpha-2 codes, matching the ISO entries in flag-icons country.json.
const countryCodes = " AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW "

func (s *Service) UpdateCountryOverride(ctx context.Context, id int64, code *string) (Server, error) {
	if code != nil {
		raw := strings.TrimSpace(*code)
		if raw == "" {
			code = nil
		} else {
			if len(raw) != 2 || !asciiCountryLetter(raw[0]) || !asciiCountryLetter(raw[1]) {
				return Server{}, ErrInvalidCountryOverride
			}
			normalized := strings.ToUpper(raw)
			if !strings.Contains(countryCodes, " "+normalized+" ") {
				return Server{}, ErrInvalidCountryOverride
			}
			code = &normalized
		}
	}
	if err := s.EnsureMutable(ctx, id); err != nil {
		return Server{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE servers SET country_override_code = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL AND decommission_status = ''`, code, s.now().UTC().Truncate(time.Second).Unix(), id)
	if err != nil {
		return Server{}, fmt.Errorf("update server country override: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server country count: %w", err)
	}
	if count != 1 {
		if err := s.EnsureMutable(ctx, id); err != nil {
			return Server{}, err
		}
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func asciiCountryLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
