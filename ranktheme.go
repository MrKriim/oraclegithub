package main

// ─────────────────────────────────────────────────────────────────────────────
// RANK THEME — SINGLE SOURCE OF TRUTH
// ─────────────────────────────────────────────────────────────────────────────
// Every place in the app that colors/decorates a top-10 user (post cards,
// online slider, Hall of Fame leaderboard, profile avatar frame) reads from
// this one table. To change a rank's color or creature, edit ONLY the
// rankThemes slice below — every surface picks it up automatically.
//
// Admins are NOT part of this table — they always get their own fixed
// violet/purple treatment, handled separately wherever admin styling is
// needed, so they never compete with or overwrite a ranked user's slot.

type RankTheme struct {
	Rank        int
	Name        string // human-readable color name, for reference only
	Hex         string // solid color used for borders/rings/badges
	HexTo       string // second gradient stop (a lighter/darker shade of Hex)
	TextHex     string // slightly darker shade for readable text on white
	TextHexDark string // lighter shade for readable text in dark mode
	Creature    string // emoji shown next to the username
	AnimClass   string // CSS animation class for the creature (post cards + leaderboard)
}

// rankThemes is indexed 1-10. EDIT HERE to change any rank's look everywhere.
// Gradients are applied as inline CSS (not Tailwind classes) so they can
// never be purged/dropped by a JIT class scanner — every rank is guaranteed
// to render its color regardless of build tooling.
var rankThemes = [51]RankTheme{
	{}, // index 0 unused
	{1, "Amber", "#f59e0b", "#f59e0b", "#f59e0b", "#f59e0b", "🦁", "anim-bounce"},
	{2, "Blue", "#f97316", "#f97316", "#f97316", "#f97316", "🐯", "anim-wiggle"},
	{3, "Rose", "#b45309", "#b45309", "#b45309", "#b45309", "🐆", "anim-peek"},
	{4, "Indigo", "#3b82f6", "#3b82f6", "#3b82f6", "#3b82f6", "🐺", "anim-bounce"},
	{5, "Cyan", "#06b6d4", "#06b6d4", "#06b6d4", "#06b6d4", "🦅", "anim-float"},
	{6, "Emerald", "#10b981", "#10b981", "#10b981", "#10b981", "🐻", "anim-wiggle"},
	{7, "Green", "#84cc16", "#84cc16", "#84cc16", "#84cc16", "🦈", "anim-float"},
	{8, "Stone", "#64748b", "#64748b", "#64748b", "#64748b", "🦍", "anim-bounce"},
	{9, "Pink", "#f43f5e", "#f43f5e", "#f43f5e", "#f43f5e", "🐲", "anim-peek"},
	{10, "Purple", "#d946ef", "#d946ef", "#d946ef", "#d946ef", "🦊", "anim-wiggle"},
	{11, "Teal", "#14b8a6", "#14b8a6", "#0f766e", "#2dd4bf", "🐢", "anim-float"},
	{12, "Violet", "#8b5cf6", "#8b5cf6", "#7c3aed", "#a78bfa", "🦄", "anim-bounce"},
	{13, "Sky", "#0ea5e9", "#0ea5e9", "#0369a1", "#38bdf8", "🐬", "anim-wiggle"},
	{14, "Crimson", "#ef4444", "#ef4444", "#b91c1c", "#f87171", "🦦", "anim-peek"},
	{15, "Gold", "#eab308", "#eab308", "#a16207", "#facc15", "🐝", "anim-float"},
	{16, "Lime", "#22c55e", "#22c55e", "#15803d", "#4ade80", "🐰", "anim-bounce"},
	{17, "Copper", "#92400e", "#92400e", "#78350f", "#c2703d", "🦔", "anim-wiggle"},
	{18, "Slate", "#6b7280", "#6b7280", "#374151", "#9ca3af", "🦫", "anim-peek"},
	{19, "Magenta", "#ec4899", "#ec4899", "#be185d", "#f472b6", "🦩", "anim-float"},
	{20, "Bronze", "#ca8a04", "#ca8a04", "#854d0e", "#eab308", "🐘", "anim-bounce"},
	{21, "Plum", "#a855f7", "#a855f7", "#7e22ce", "#c084fc", "🦉", "anim-wiggle"},
	{22, "Steel", "#475569", "#475569", "#1e293b", "#94a3b8", "🐨", "anim-peek"},
	{23, "Saffron", "#f59e0b", "#f59e0b", "#b45309", "#fbbf24", "🦒", "anim-float"},
	{24, "Ice", "#38bdf8", "#38bdf8", "#0284c7", "#7dd3fc", "🐧", "anim-bounce"},
	{25, "Charcoal", "#1f2937", "#1f2937", "#0b0f16", "#4b5563", "🦓", "anim-wiggle"},
	{26, "Swamp", "#15803d", "#15803d", "#052e16", "#4ade80", "🦜", "anim-peek"},
	{27, "Orchid", "#d946ef", "#d946ef", "#86198f", "#f0abfc", "🦋", "anim-float"},
	{28, "Abyss", "#1e3a8a", "#1e3a8a", "#0c1a3e", "#60a5fa", "🐙", "anim-bounce"},
	{29, "Venom", "#65a30d", "#65a30d", "#365314", "#a3e635", "🐞", "anim-wiggle"},
	{30, "Jade", "#059669", "#059669", "#022c22", "#34d399", "🐠", "anim-peek"},
	{31, "Chestnut", "#9a3412", "#9a3412", "#431407", "#fb923c", "🐿️", "anim-float"},
	{32, "Ash", "#57534e", "#57534e", "#1c1917", "#a8a29e", "🦝", "anim-bounce"},
	{33, "Blush", "#fb7185", "#fb7185", "#9f1239", "#fda4af", "🐈", "anim-wiggle"},

	{34, "Forest", "#166534", "#166534", "#052e16", "#4ade80", "🦌", "anim-peek"},
	{35, "Viper", "#4d7c0f", "#4d7c0f", "#1a2e05", "#a3e635", "🐼", "anim-float"},
	{36, "Midnight", "#312e81", "#312e81", "#1e1b4b", "#818cf8", "🐥", "anim-bounce"},
	{37, "Ocean", "#0369a1", "#0369a1", "#082f49", "#38bdf8", "🐋", "anim-wiggle"},
	{38, "Pearl", "#64748b", "#64748b", "#334155", "#cbd5e1", "🦭", "anim-peek"},
	{39, "Sand", "#b45309", "#b45309", "#78350f", "#fbbf24", "🐹", "anim-float"},
	{40, "Dune", "#d97706", "#d97706", "#92400e", "#fcd34d", "🐫", "anim-bounce"},
	{41, "Coral", "#fb7185", "#fb7185", "#be123c", "#fda4af", "🦘", "anim-wiggle"},
	{42, "Wool", "#a8a29e", "#a8a29e", "#57534e", "#d6d3d1", "🦙", "anim-peek"},
	{43, "Meadow", "#65a30d", "#65a30d", "#3f6212", "#bef264", "🐑", "anim-float"},
	{44, "Rosewood", "#be123c", "#be123c", "#4c0519", "#fb7185", "🦖", "anim-bounce"},
	{45, "Sage", "#4d7c0f", "#4d7c0f", "#1a2e05", "#84cc16", "🦕", "anim-wiggle"},
	{46, "Brick", "#b91c1c", "#b91c1c", "#450a0a", "#f87171", "🐌", "anim-peek"},
	{47, "Harvest", "#c2703d", "#c2703d", "#7c2d12", "#fb923c", "🐦", "anim-float"},
	{48, "Pond", "#0d9488", "#0d9488", "#042f2e", "#5eead4", "🦆", "anim-bounce"},
	{49, "Frost", "#7dd3fc", "#7dd3fc", "#0369a1", "#bae6fd", "🦢", "anim-wiggle"},
	{50, "Royal", "#6d28d9", "#6d28d9", "#3b0764", "#c4b5fd", "🦚", "anim-peek"},
}

// rankTitles mirrors rankThemes 1-50 for the leaderboard's title line.
var rankTitles = [51]string{
	"", "The Lion King", "Royal Tiger", "Swift Cheetah", "Lone Wolf",
	"Sky Eagle", "Mighty Bear", "Great Shark", "Noble Horse",
	"Hidden Dragon", "Sly Fox", "Steady Turtle", "Mythic Unicorn",
	"Clever Dolphin", "Playful Otter", "Busy Bee", "Bouncy Bunny",
	"Sharp Hedgehog", "Clever Beaver", "Pink Flamingo", "Wise Elephant",
	"Wise Owl", "Cuddly Koala", "Tall Giraffe", "Cool Penguin",
	"Striped Zebra", "Chatty Parrot", "Free Butterfly", "Deep Octopus",
	"Lucky Ladybug", "Rainbow Fish", "Busy Chipmunk", "Curious Raccoon",
	"Sweet Kitten", "Graceful Deer", "Cuddly Panda", "Fluffy Chick",
	"Gentle Whale", "Smooth Seal", "Tiny Hamster", "Desert Camel",
	"Bouncy Kangaroo", "Proud Llama", "Fluffy Lamb", "Tiny T-Rex",
	"Friendly Dino", "Gentle Snail", "Happy Songbird", "Calm Duck",
	"Elegant Swan", "Regal Peacock",
}
// AdminTheme is the fixed, non-competing style for admin accounts.
var AdminTheme = RankTheme{
	Hex: "#8b5cf6", TextHex: "#7c3aed", Creature: "👑",
}

// GetRankTheme returns the theme for rank 1-10, or a zero-value RankTheme
// (Hex == "") for anything outside that range — callers should check
// theme.Hex != "" before applying rank styling.
func GetRankTheme(rank int) RankTheme {
	if rank >= 1 && rank <= 50 {
		return rankThemes[rank]
	}
	return RankTheme{}
}

func GetRankTitle(rank int) string {
	if rank >= 1 && rank <= 50 {
		return rankTitles[rank]
	}
	return "Legend"
}
// LeaderboardStyle returns an inline background-gradient + border-color CSS
// string for the Hall of Fame card, guaranteed to render since it bypasses
// Tailwind's class scanner entirely.
func (t RankTheme) LeaderboardStyle(isFirst bool) string {
	if t.Hex == "" {
		return "background:#fff;border-color:#f3f4f6;"
	}
	scale := ""
	if isFirst {
		scale = "transform:scale(1.06);z-index:10;"
	}
	return "background:linear-gradient(90deg," + t.Hex + "," + t.HexTo + ");border-color:" + t.HexTo + ";" + scale
}

// rankFrameClass returns the CSS class used by rankFrameCSS in post_renderer.go
func (t RankTheme) FrameClass() string {
	if t.Hex == "" {
		return ""
	}
	return "rank-frame rank-frame-" + itoa(t.Rank)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
