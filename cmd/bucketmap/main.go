// Command bucketmap checks how an API answers and rate limits every Discord
// REST route a bot can reach: Discord itself, or a candidate standing in front
// of it or in its place.
//
//	bucketmap run -token "$TOKEN" -guild 123 -users 1,2,3,4 -report direct.json
//	bucketmap run -api http://localhost:8080/api/v10 ... -report candidate.json
//	bucketmap compare direct.json candidate.json
//	bucketmap coverage
//	bucketmap index -spec openapi.json -userdoccers pages
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/FCAgreatgoals/bucketmap/internal/engine"
	"github.com/FCAgreatgoals/bucketmap/internal/indexgen"
	"github.com/FCAgreatgoals/bucketmap/routes"
)

// version is set at build time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "compare":
		os.Exit(compareCmd(os.Args[2:]))
	case "coverage":
		os.Exit(coverageCmd())
	case "index":
		os.Exit(indexCmd(os.Args[2:]))
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  bucketmap run       walk a test guild through every route and report
  bucketmap compare   put a run against Discord and a run against a candidate side by side
  bucketmap coverage  list which routes the scenario exercises
  bucketmap index     rebuild routes/index.json
  bucketmap version
`)
	os.Exit(2)
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	api := fs.String("api", "https://discord.com/api/v10", "API root: Discord itself, or the candidate")
	token := fs.String("token", os.Getenv("TOKEN"), "bot token, without the Bot prefix (default $TOKEN)")
	guild := fs.String("guild", "", "id of the guild set aside for testing")
	users := fs.String("users", "", "comma separated ids of the four test users")
	marker := fs.String("marker", "bucketmap", "text the guild name must contain before anything is touched")
	duration := fs.Duration("duration", 10*time.Minute, "how long to spread the run over")
	allowKick := fs.Bool("allow-kick", false, "kick test user 3, who then has to rejoin")
	allowBan := fs.Bool("allow-ban", false, "ban then unban test user 4, who then has to rejoin")
	allowPrune := fs.Bool("allow-prune", false, "prune members inactive for thirty days who hold no role")
	allowGlobal := fs.Bool("allow-global-commands", false, "create and delete a global command, seen for a moment by every guild of the bot")
	out := fs.String("report", "bucketmap.json", "where to write the report")
	fs.Parse(args)

	r, err := engine.Run(engine.Config{
		API: *api, Token: *token, Guild: *guild, Users: splitIDs(*users), Marker: *marker,
		Duration: *duration, Agent: "DiscordBot (https://github.com/FCAgreatgoals/bucketmap, " + version + ")",
		AllowKick: *allowKick, AllowBan: *allowBan, AllowPrune: *allowPrune, AllowGlobalCommands: *allowGlobal,
	})
	if r != nil {
		if raw, err := json.MarshalIndent(r, "", "  "); err == nil {
			_ = os.WriteFile(*out, raw, 0o644)
		}
		r.Print(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bucketmap:", err)
		return 2
	}
	if len(r.Failures) > 0 || len(r.TooMany()) > 0 {
		return 1
	}
	return 0
}

func compareCmd(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: bucketmap compare direct.json candidate.json")
		return 2
	}
	direct, err := engine.LoadReport(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	candidate, err := engine.LoadReport(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if engine.Compare(os.Stdout, direct, candidate) {
		return 0
	}
	return 1
}

func coverageCmd() int {
	all, err := routes.All()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	byCoverage := map[routes.Coverage][]routes.Route{}
	for _, r := range all {
		byCoverage[r.Coverage] = append(byCoverage[r.Coverage], r)
	}
	order := []routes.Coverage{routes.Exercised, routes.Gated, routes.NeedsInteraction, routes.NotExercised, routes.OutOfScope}
	fmt.Printf("%d routes indexed\n", len(all))
	for _, c := range order {
		fmt.Printf("  %-18s %d\n", c, len(byCoverage[c]))
	}
	for _, c := range order[1:4] {
		list := byCoverage[c]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
		fmt.Printf("\n%s\n", c)
		for _, r := range list {
			fmt.Printf("  %-6s %s\n", r.Method, r.Path)
		}
	}
	return 0
}

func indexCmd(args []string) int {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	spec := fs.String("spec", "", "Discord's openapi.json (github.com/discord/discord-api-spec)")
	ud := fs.String("userdoccers", "", "the pages directory of github.com/discord-userdoccers/discord-userdoccers")
	ann := fs.String("annotations", "routes/annotations.json", "hand-written annotations")
	out := fs.String("out", "routes/index.json", "where to write the index")
	fs.Parse(args)
	if *spec == "" || *ud == "" {
		fmt.Fprintln(os.Stderr, "-spec and -userdoccers are required")
		return 2
	}
	n, err := indexgen.Write(indexgen.Sources{Spec: *spec, Userdoccers: *ud, Annotations: *ann}, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bucketmap:", err)
		return 1
	}
	fmt.Printf("%d routes written to %s\n", n, *out)
	return 0
}

func splitIDs(s string) []string {
	var ids []string
	for _, id := range strings.Split(s, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
