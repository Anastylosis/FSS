package czechav

import (
	"github.com/Anastylosis/FSS/internal/scrapers/czechavutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []czechavutil.SiteConfig{
	// CzechAV Network
	{SiteID: "czechamateurs", Domain: "czechamateurs.com", Studio: "Czech Amateurs"},
	{SiteID: "czechbangbus", Domain: "czechbangbus.com", Studio: "Czech Bang Bus"},
	{SiteID: "czechbitch", Domain: "czechbitch.com", Studio: "Czech Bitch"},
	{SiteID: "czechcabins", Domain: "czechcabins.com", Studio: "Czech Cabins"},
	{SiteID: "czechcasting", Domain: "czechcasting.com", Studio: "Czech Casting"},
	{SiteID: "czechcouples", Domain: "czechcouples.com", Studio: "Czech Couples"},
	{SiteID: "czechdellais", Domain: "czechdellais.com", Studio: "Czech Dellais"},
	{SiteID: "czechdungeon", Domain: "czechdungeon.com", Studio: "Czech Dungeon"},
	{SiteID: "czechestrogenolit", Domain: "czechestrogenolit.com", Studio: "Czech Estrogenolit"},
	{SiteID: "czechexperiment", Domain: "czechexperiment.com", Studio: "Czech Experiment"},
	{SiteID: "czechfantasy", Domain: "czechfantasy.com", Studio: "Czech Fantasy"},
	{SiteID: "czechfirstvideo", Domain: "czechfirstvideo.com", Studio: "Czech First Video"},
	{SiteID: "czechgangbang", Domain: "czechgangbang.com", Studio: "Czech Gang Bang"},
	{SiteID: "czechgardenparty", Domain: "czechgardenparty.com", Studio: "Czech Garden Party"},
	{SiteID: "czechharem", Domain: "czechharem.com", Studio: "Czech Harem"},
	{SiteID: "czechhomeorgy", Domain: "czechhomeorgy.com", Studio: "Czech Home Orgy"},
	{SiteID: "czechhypno", Domain: "czechhypno.com", Studio: "Czech Hypno"},
	{SiteID: "czechjacker", Domain: "czechjacker.com", Studio: "Czech Jacker"},
	{SiteID: "czechlesbians", Domain: "czechlesbians.com", Studio: "Czech Lesbians"},
	{SiteID: "czechmassage", Domain: "czechmassage.com", Studio: "Czech Massage"},
	{SiteID: "czechmegaswingers", Domain: "czechmegaswingers.com", Studio: "Czech Mega Swingers"},
	{SiteID: "czechorgasm", Domain: "czechorgasm.com", Studio: "Czech Orgasm"},
	{SiteID: "czechparties", Domain: "czechparties.com", Studio: "Czech Parties"},
	{SiteID: "czechpawnshop", Domain: "czechpawnshop.com", Studio: "Czech Pawn Shop"},
	{SiteID: "czechpool", Domain: "czechpool.com", Studio: "Czech Pool"},
	{SiteID: "czechsauna", Domain: "czechsauna.com", Studio: "Czech Sauna"},
	{SiteID: "czechsharking", Domain: "czechsharking.com", Studio: "Czech Sharking"},
	{SiteID: "czechsnooper", Domain: "czechsnooper.com", Studio: "Czech Snooper"},
	{SiteID: "czechsolarium", Domain: "czechsolarium.com", Studio: "Czech Solarium"},
	{SiteID: "czechspy", Domain: "czechspy.com", Studio: "Czech Spy"},
	{SiteID: "czechstreets", Domain: "czechstreets.com", Studio: "Czech Streets"},
	{SiteID: "czechstudents", Domain: "czechstudents.com", Studio: "Czech Students"},
	{SiteID: "czechsupermodels", Domain: "czechsupermodels.com", Studio: "Czech Super Models"},
	{SiteID: "czechtantra", Domain: "czechtantra.com", Studio: "Czech Tantra"},
	{SiteID: "czechtaxi", Domain: "czechtaxi.com", Studio: "Czech Taxi"},
	{SiteID: "czechwifeswap", Domain: "czechwifeswap.com", Studio: "Czech Wife Swap"},

	// GoPerv Network
	{SiteID: "clubbdsm", Domain: "clubbdsm.com", Studio: "Club BDSM"},
	{SiteID: "dirtysarah", Domain: "dirtysarah.com", Studio: "Dirty Sarah"},
	{SiteID: "extremestreets", Domain: "extremestreets.com", Studio: "Extreme Streets"},
	{SiteID: "horrorporn", Domain: "horrorporn.com", Studio: "Horror Porn"},
	{SiteID: "perversefamily", Domain: "perversefamily.com", Studio: "Perverse Family"},
	{SiteID: "perversefamilylive", Domain: "perversefamilylive.com", Studio: "Perverse Family Live"},
	{SiteID: "powerfetish", Domain: "powerfetish.com", Studio: "Power Fetish"},
	{SiteID: "selfiefetish", Domain: "selfiefetish.com", Studio: "Selfie Fetish"},
	{SiteID: "xvirtual", Domain: "xvirtual.com", Studio: "xVirtual"},

	// Czech Gay Authentic Videos
	{SiteID: "czechgayamateurs", Domain: "czechgayamateurs.com", Studio: "Czech Gay Amateurs"},
	{SiteID: "czechgaycasting", Domain: "czechgaycasting.com", Studio: "Czech Gay Casting"},
	{SiteID: "czechgaycouples", Domain: "czechgaycouples.com", Studio: "Czech Gay Couples"},
	{SiteID: "czechgayfantasy", Domain: "czechgayfantasy.com", Studio: "Czech Gay Fantasy"},
	{SiteID: "czechgaymassage", Domain: "czechgaymassage.com", Studio: "Czech Gay Massage"},
	{SiteID: "czechgaysolarium", Domain: "czechgaysolarium.com", Studio: "Czech Gay Solarium"},
	{SiteID: "gayhorrorporn", Domain: "gayhorrorporn.com", Studio: "Gay Horror Porn"},

	// Other
	{SiteID: "annalovesshemale", Domain: "annalovesshemale.com", Studio: "Anna Loves Shemale"},
	{SiteID: "creativeporn", Domain: "creativeporn.com", Studio: "Creative Porn"},
	{SiteID: "lifepornstories", Domain: "lifepornstories.com", Studio: "Life Porn Stories"},
	{SiteID: "mikebigdick", Domain: "mikebigdick.com", Studio: "Mike Big Dick"},
	{SiteID: "monstercockgang", Domain: "monstercockgang.com", Studio: "Monster Cock Gang"},
	{SiteID: "movieporn", Domain: "movieporn.com", Studio: "Movie Porn"},
	{SiteID: "r51", Domain: "r51.com", Studio: "R51"},
	{SiteID: "redneckjohn", Domain: "redneckjohn.com", Studio: "Redneck John"},
	{SiteID: "unrealporn", Domain: "unrealporn.com", Studio: "Unreal Porn"},
	{SiteID: "unusualpeople", Domain: "unusualpeople.com", Studio: "Unusual People"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(czechavutil.New(cfg))
	}
}
