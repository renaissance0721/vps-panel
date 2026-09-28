package subscription

import "fmt"

func FormatNodeDisplayName(name string, multiplierBP int) string {
	whole := multiplierBP / 100
	fraction := multiplierBP % 100
	var multiplier string
	switch {
	case fraction == 0:
		multiplier = fmt.Sprintf("%d", whole)
	case fraction%10 == 0:
		multiplier = fmt.Sprintf("%d.%d", whole, fraction/10)
	default:
		multiplier = fmt.Sprintf("%d.%02d", whole, fraction)
	}
	return fmt.Sprintf("%s [%s×]", name, multiplier)
}
