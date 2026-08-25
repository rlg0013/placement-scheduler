package generator

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"placement-scheduler/pkg/models"
)

// Config controls the scale and shape of the generated placement week.
// Defaults below mirror the assignment's stated scenario (35 companies,
// 800 students, 4 days, 20 rooms) but every knob is exposed so you can
// dial it down while testing your scheduler on a smaller instance.
type Config struct {
	Seed int64

	NumRooms    int
	NumStudents int

	// Company counts per tier. Tier composition itself is a realism
	// claim: mass recruiters are FEW but each shortlists HUNDREDS;
	// niche companies are MORE numerous but each shortlists a HANDFUL.
	// This mirrors real placement season — a handful of giants (product
	// companies, service giants) dominate volume on Day 1, while dozens
	// of smaller/specialized companies pick narrowly later in the week.
	NumMassRecruiters int
	NumMidTier        int
	NumNiche          int

	PlacementStartDate time.Time // Day 1's date; days run sequentially from here
}

var IST = time.FixedZone("IST", 5*3600+30*60)

func DefaultConfig() Config {
	return Config{
		Seed:               42,
		NumRooms:           20,
		NumStudents:        800,
		NumMassRecruiters:  8,
		NumMidTier:         17,
		NumNiche:           10, // 8+17+10 = 35 companies total
		PlacementStartDate: time.Date(2026, 9, 1, 9, 0, 0, 0, IST),
	}
}

// tierProfile holds the realism parameters that differ by company tier.
// Centralizing these numbers here (instead of scattering magic numbers
// through the generation code) is what makes them easy to defend and
// easy to tune later.
type tierProfile struct {
	cgpaCutoffMin, cgpaCutoffMax       float64 // mass recruiters cast a wide net -> low cutoff
	numPanelsMin, numPanelsMax         int     // mass recruiters run many parallel panels
	durationMinutes                    int     // niche companies run longer, deeper interviews
	shortlistSizeMin, shortlistSizeMax int     // how many students this company shortlists
	dayOffset                          int     // FIRST eligible placement day (0-indexed) for this tier
	numEligibleDays                    int     // how many consecutive days this tier can land on (round-robin spread)
	slotHours                          float64 // how many hours of interviewing this company gets, per wave

	// selectionNoiseStd controls HOW a company picks from its eligible
	// (CGPA >= cutoff) pool. Small noise -> selection tracks CGPA
	// closely (a sharp, elitist ranking). Large noise -> selection is
	// closer to uniform among everyone who cleared the bar. Real mass
	// recruiters don't rank hard above their cutoff (they want volume,
	// not just toppers); real niche/product companies genuinely pick
	// the best of the best. Modeling this PER TIER, not as one global
	// constant, is what keeps top students from monopolizing the
	// volume hirers that exist specifically to give average students
	// a shot.
	selectionNoiseStd float64
}

func profileFor(tier models.PriorityTier) tierProfile {
	switch tier {
	case models.TierMassRecruiter:
		// Real placement week pattern: the biggest recruiters (mass IT
		// services, high-volume product companies) go on Day 1, cast
		// the widest net (low CGPA cutoff), and need many panels
		// running in parallel to get through hundreds of candidates in
		// one day with short interviews.
		return tierProfile{
			cgpaCutoffMin: 5.5, cgpaCutoffMax: 6.5,
			numPanelsMin: 4, numPanelsMax: 6, // restored from 3-5 now that waves handle room contention
			durationMinutes:  15,
			shortlistSizeMin: 60, shortlistSizeMax: 90, // raised alongside panel count to actually widen coverage, not just capacity
			dayOffset:         0,
			slotHours:         4,   // half-day: Day 1 is split into AM/PM waves so 20 rooms can serve ~40 panels across the day
			selectionNoiseStd: 2.5, // near-uniform above cutoff: volume over ranking
		}
	case models.TierMidTier:
		return tierProfile{
			cgpaCutoffMin: 6.5, cgpaCutoffMax: 7.5,
			numPanelsMin: 2, numPanelsMax: 3,
			durationMinutes:  25,
			shortlistSizeMin: 30, shortlistSizeMax: 55, // tuned down from 50-120: was ~2-3x oversubscribed against panel capacity
			dayOffset:         1, // Day 2, could also land on Day 3 — randomized in caller
			slotHours:         7,
			selectionNoiseStd: 1.0, // moderate ranking
		}
	default: // TierNiche
		// Niche/product companies interview last, pick the fewest
		// students, demand the highest CGPA, and run long deep-dive
		// interviews (system design, domain-specific rounds).
		return tierProfile{
			cgpaCutoffMin: 7.5, cgpaCutoffMax: 8.8,
			numPanelsMin: 1, numPanelsMax: 2,
			durationMinutes:  40,
			shortlistSizeMin: 10, shortlistSizeMax: 18, // tuned down from 15-40 to roughly match panel capacity
			dayOffset:         2, // Day 3/4 — randomized in caller
			slotHours:         6,
			selectionNoiseStd: 0.4, // sharp ranking: genuinely picks the best of the best
		}
	}
}

var branches = []models.Branch{"CSE", "ISE", "ECE", "MECH", "CIVIL", "EEE"}

// Generate produces a full ShortlistData instance: rooms, students,
// companies, panels, and the shortlist relationships between them.
func Generate(cfg Config) models.ShortlistData {
	rng := rand.New(rand.NewSource(cfg.Seed))

	rooms := generateRooms(cfg.NumRooms)
	students := generateStudents(rng, cfg.NumStudents)

	companies := map[string]models.Company{}
	panels := map[string]models.Panel{}

	addCompanies := func(count int, tier models.PriorityTier, namePrefix string) {
		for i := 0; i < count; i++ {
			profile := profileFor(tier)
			companyID := fmt.Sprintf("%s-%02d", namePrefix, i+1)

			var slotStart, slotEnd time.Time

			if tier == models.TierMassRecruiter {
				// Day 1 alone has ~32-40 panels wanting a room at once
				// against only 20 rooms — splitting into an AM and PM
				// wave (alternating by index) means the same 20 rooms
				// serve roughly double the panel-count across the day,
				// since AM panels vacate before PM panels need a room.
				dayStart := cfg.PlacementStartDate.AddDate(0, 0, profile.dayOffset)
				wave := i % 2
				if wave == 0 {
					slotStart = dayStart // e.g. 9:00
				} else {
					slotStart = dayStart.Add(time.Duration(profile.slotHours) * time.Hour) // e.g. 13:00
				}
				slotEnd = slotStart.Add(time.Duration(profile.slotHours * float64(time.Hour)))
			} else {
				// Mid/Niche tiers spread across days 2-4. Rather than a
				// 50/50 coin flip per company (which can accidentally
				// dump a wildly uneven load on one day), the split is
				// deliberately weighted to shape the week on purpose:
				// Day 2 gets the bulk of mid-tier (a real "second wave"
				// rush after mass recruiters clear out) and is meant to
				// be genuinely oversubscribed — your best live demo of
				// defending an infeasible day. Day 4 is deliberately
				// slack (niche-only, mostly), mirroring how placement
				// weeks taper off — and gives you a real "quieter day"
				// to contrast against Day 2 in your metrics.
				var dayOffset int
				if tier == models.TierMidTier {
					threshold := int(math.Ceil(float64(count) * 0.65)) // ~65% -> Day 2 (the crunch)
					if i < threshold {
						dayOffset = profile.dayOffset // Day 2
					} else {
						dayOffset = profile.dayOffset + 1 // Day 3
					}
				} else { // TierNiche
					threshold := int(math.Round(float64(count) * 0.3)) // ~30% -> Day 3, rest Day 4
					if i < threshold {
						dayOffset = profile.dayOffset // Day 3
					} else {
						dayOffset = profile.dayOffset + 1 // Day 4
					}
				}
				dayStart := cfg.PlacementStartDate.AddDate(0, 0, dayOffset)
				slotStart = dayStart
				slotEnd = dayStart.Add(time.Duration(profile.slotHours * float64(time.Hour)))
			}

			cutoff := profile.cgpaCutoffMin + rng.Float64()*(profile.cgpaCutoffMax-profile.cgpaCutoffMin)
			numPanels := profile.numPanelsMin + rng.Intn(profile.numPanelsMax-profile.numPanelsMin+1)

			panelIDs := make([]string, 0, numPanels)
			for p := 0; p < numPanels; p++ {
				panelID := fmt.Sprintf("%s-P%d", companyID, p+1)
				panels[panelID] = models.Panel{ID: panelID, CompanyID: companyID}
				panelIDs = append(panelIDs, panelID)
			}

			companies[companyID] = models.Company{
				ID:                companyID,
				Name:              fmt.Sprintf("%s (%s)", companyID, tierName(tier)),
				CGPACutoff:        round1(cutoff),
				Tier:              tier,
				InterviewDuration: time.Duration(profile.durationMinutes) * time.Minute,
				Panels:            panelIDs,
				Slots:             []models.TimeSlot{{Start: slotStart, End: slotEnd}},
			}
		}
	}

	addCompanies(cfg.NumMassRecruiters, models.TierMassRecruiter, "MASS")
	addCompanies(cfg.NumMidTier, models.TierMidTier, "MID")
	addCompanies(cfg.NumNiche, models.TierNiche, "NICHE")

	shortlists := generateShortlists(rng, companies, students)

	return models.ShortlistData{
		Companies:  companies,
		Panels:     panels,
		Rooms:      rooms,
		Students:   students,
		Shortlists: shortlists,
	}
}

func generateRooms(n int) map[string]models.Room {
	rooms := make(map[string]models.Room, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("R%02d", i+1)
		rooms[id] = models.Room{ID: id, Capacity: 1}
	}
	return rooms
}

func generateStudents(rng *rand.Rand, n int) map[string]models.Student {
	students := make(map[string]models.Student, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("S%04d", i+1)
		// CGPA drawn from a rough bell shape (sum of uniforms) rather
		// than pure uniform — real CGPA distributions cluster around a
		// mid-range with fewer students at the extremes. This matters
		// because a pure-uniform CGPA would make every company's
		// cutoff slice off a suspiciously exact, linear fraction of
		// students — real cohorts aren't that clean.
		cgpa := approxNormal(rng, 7.2, 0.9)
		if cgpa < 5.0 {
			cgpa = 5.0
		}
		if cgpa > 9.9 {
			cgpa = 9.9
		}
		students[id] = models.Student{
			ID:     id,
			Name:   fmt.Sprintf("Student-%04d", i+1),
			CGPA:   round1(cgpa),
			Branch: branches[rng.Intn(len(branches))],
		}
	}
	return students
}

// generateShortlists is where the "top students appear on many
// overlapping lists" realism requirement actually needs to show up
// clearly, not just weakly. It has two layers:
//
//  1. CGPA-cutoff eligibility (a student below cutoff can never be
//     shortlisted) — this alone creates SOME overlap.
//  2. Within the eligible pool, selection is CGPA-weighted, not
//     uniform. Real recruiters don't pick randomly among everyone who
//     clears the bar — they skew hard toward the strongest resumes.
//     We add noise so it's not a rigid CGPA ranking (branch fit and
//     other factors matter too), but a 9.5 CGPA student should win
//     against a 6.5 CGPA student almost every time, not 50/50.
func generateShortlists(rng *rand.Rand, companies map[string]models.Company, students map[string]models.Student) []models.ShortlistEntry {
	var shortlists []models.ShortlistEntry

	type scored struct {
		id    string
		score float64
	}

	companyIDs := make([]string, 0, len(companies))
	for id := range companies {
		companyIDs = append(companyIDs, id)
	}
	sort.Strings(companyIDs)

	studentIDs := make([]string, 0, len(students))
	for id := range students {
		studentIDs = append(studentIDs, id)
	}
	sort.Strings(studentIDs)

	for _, companyID := range companyIDs {
		company := companies[companyID]
		profile := profileFor(company.Tier)

		eligible := make([]scored, 0, len(students))
		for _, id := range studentIDs {
			s := students[id]
			if s.CGPA >= company.CGPACutoff {
				noise := rng.NormFloat64() * profile.selectionNoiseStd
				eligible = append(eligible, scored{id: id, score: s.CGPA + noise})
			}
		}
		sort.Slice(eligible, func(i, j int) bool { return eligible[i].score > eligible[j].score })

		target := profile.shortlistSizeMin + rng.Intn(profile.shortlistSizeMax-profile.shortlistSizeMin+1)
		if target > len(eligible) {
			target = len(eligible)
		}
		for _, e := range eligible[:target] {
			shortlists = append(shortlists, models.ShortlistEntry{CompanyID: company.ID, StudentID: e.id})
		}
	}

	return shortlists
}

// approxNormal approximates a normal distribution using the
// irwin-hall/CLT trick (sum of uniforms) — good enough for realistic
// synthetic data without pulling in a stats library.
func approxNormal(rng *rand.Rand, mean, stddev float64) float64 {
	sum := 0.0
	const k = 6
	for i := 0; i < k; i++ {
		sum += rng.Float64()
	}
	// sum of k uniforms has mean k/2, variance k/12, so stddev is sqrt(k/12).
	// Dividing by that stddev gives an approximately standard normal value.
	z := (sum - k/2.0) / math.Sqrt(k/12.0)
	return mean + z*stddev
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func tierName(t models.PriorityTier) string {
	switch t {
	case models.TierMassRecruiter:
		return "Mass Recruiter"
	case models.TierMidTier:
		return "Mid-Tier"
	default:
		return "Niche"
	}
}
