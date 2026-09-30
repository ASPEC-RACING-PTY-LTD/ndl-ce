package gameserver

// gameArt is keyed by Template.GameTitle. See game_art.go for how each
// entry was verified; the comment on each line is the evidence URL.
var gameArt = map[string]GameArt{
	"7 Days to Die":                          {SteamAppID: "251570"},             // https://store.steampowered.com/app/251570/7_Days_to_Die/
	"Action: Source":                         {SteamAppID: "977050"},             // https://store.steampowered.com/app/977050/Action_Source/
	"American Truck Simulator":               {SteamAppID: "270880"},             // https://store.steampowered.com/app/270880/American_Truck_Simulator/
	"Among Us":                               {SteamAppID: "945360"},             // https://store.steampowered.com/app/945360/Among_Us/
	"ARK: Survival Evolved":                  {SteamAppID: "346110"},             // https://store.steampowered.com/app/346110/ARK_Survival_Evolved/
	"Arma 3":                                 {SteamAppID: "107410"},             // https://store.steampowered.com/app/107410/Arma_3/
	"Arma Reforger":                          {SteamAppID: "1874880"},            // https://store.steampowered.com/app/1874880/Arma_Reforger/
	"AssaultCube":                            {GitHub: "assaultcube"},            // Not on Steam; github.com/assaultcube avatar is the AssaultCube logo (white cube on red splatter)
	"Assetto Corsa":                          {SteamAppID: "244210"},             // https://store.steampowered.com/app/244210/Assetto_Corsa/
	"Astro Colony":                           {SteamAppID: "1614550"},            // https://store.steampowered.com/app/1614550/Astro_Colony/
	"Avorion":                                {SteamAppID: "445220"},             // https://store.steampowered.com/app/445220/Avorion/
	"Ballistic Overkill":                     {SteamAppID: "296300"},             // https://store.steampowered.com/app/296300/Ballistic_Overkill/
	"Banana Shooter":                         {SteamAppID: "1949740"},            // https://store.steampowered.com/app/1949740/Banana_Shooter/
	"Barotrauma":                             {SteamAppID: "602960"},             // https://store.steampowered.com/app/602960/Barotrauma/
	"Base Defense":                           {SteamAppID: "632730"},             // https://store.steampowered.com/app/632730/Base_Defense/
	"BATTALION: Legacy":                      {SteamAppID: "489940"},             // https://store.steampowered.com/app/489940/BATTALION_Legacy/
	"BeamNG.drive":                           {SteamAppID: "284160"},             // https://store.steampowered.com/app/284160/BeamNGdrive/
	"Black Mesa":                             {SteamAppID: "362890"},             // https://store.steampowered.com/app/362890/Black_Mesa/
	"Blade Symphony":                         {SteamAppID: "225600"},             // https://store.steampowered.com/app/225600/Blade_Symphony/
	"BrainBread":                             {GitHub: "IronOak-Studios"},        // Original GoldSrc BrainBread has no Steam app store page (only sub/9007 package); IronOak-Studios is the developer and owns github.com/IronOak-Studios/BrainBread; avatar is the IronOak studio wordmark logo (not identicon).
	"BrainBread 2":                           {SteamAppID: "346330"},             // https://store.steampowered.com/app/346330/BrainBread_2/
	"Brickadia":                              {SteamAppID: "2199420"},            // https://store.steampowered.com/app/2199420/Brickadia/
	"Chivalry: Medieval Warfare":             {SteamAppID: "219640"},             // https://store.steampowered.com/app/219640/Chivalry_Medieval_Warfare/
	"Citadel: Forged with Fire":              {SteamAppID: "487120"},             // https://store.steampowered.com/app/487120/Citadel_Forged_with_Fire/
	"Clone Hero":                             {GitHub: "clonehero-game"},         // Not on Steam; github.com/clonehero-game avatar is the Clone Hero wordmark logo
	"Codename CURE":                          {SteamAppID: "355180"},             // https://store.steampowered.com/app/355180/Codename_CURE/
	"Colony Survival":                        {SteamAppID: "366090"},             // https://store.steampowered.com/app/366090/Colony_Survival/
	"Core Keeper":                            {SteamAppID: "1621690"},            // https://store.steampowered.com/app/1621690/Core_Keeper/
	"Counter-Strike":                         {SteamAppID: "10"},                 // https://store.steampowered.com/app/10/CounterStrike/
	"Counter-Strike 2":                       {SteamAppID: "730"},                // https://store.steampowered.com/app/730/CounterStrike_2/
	"Counter-Strike: Condition Zero":         {SteamAppID: "80"},                 // https://store.steampowered.com/app/80/CounterStrike_Condition_Zero/
	"Counter-Strike: Global Offensive":       {SteamAppID: "730"},                // https://store.steampowered.com/app/730/CounterStrike_Global_Offensive/
	"Counter-Strike: Source":                 {SteamAppID: "240"},                // https://store.steampowered.com/app/240/CounterStrike_Source/
	"Craftopia":                              {SteamAppID: "1307550"},            // https://store.steampowered.com/app/1307550/Craftopia/
	"CryoFall":                               {SteamAppID: "829590"},             // https://store.steampowered.com/app/829590/CryoFall/
	"Day of Defeat":                          {SteamAppID: "30"},                 // https://store.steampowered.com/app/30/Day_of_Defeat/
	"Day of Defeat: Source":                  {SteamAppID: "300"},                // https://store.steampowered.com/app/300/Day_of_Defeat_Source/
	"Day of Dragons":                         {SteamAppID: "1088090"},            // https://store.steampowered.com/app/1088090/Day_of_Dragons/
	"Day of Infamy":                          {SteamAppID: "447820"},             // https://store.steampowered.com/app/447820/Day_of_Infamy/
	"DayZ":                                   {SteamAppID: "221100"},             // https://store.steampowered.com/app/221100/DayZ/?l=english
	"DDraceNetwork":                          {SteamAppID: "412220"},             // https://store.steampowered.com/app/412220/DDraceNetwork/
	"Deathmatch Classic":                     {SteamAppID: "40"},                 // https://store.steampowered.com/app/40/Deathmatch_Classic/
	"Don't Starve Together":                  {SteamAppID: "322330"},             // https://store.steampowered.com/app/322330/Dont_Starve_Together/
	"Doom":                                   {SteamAppID: "2280"},               // https://store.steampowered.com/app/2280/Ultimate_Doom/
	"Double Action: Boogaloo":                {SteamAppID: "317360"},             // https://store.steampowered.com/app/317360/Double_Action_Boogaloo/
	"Dystopia":                               {SteamAppID: "17580"},              // https://store.steampowered.com/app/17580/Dystopia/
	"Eco":                                    {SteamAppID: "382310"},             // https://store.steampowered.com/app/382310/Eco/
	"Empires":                                {SteamAppID: "17740"},              // https://store.steampowered.com/app/17740/Empires_Mod/
	"Euro Truck Simulator 2":                 {SteamAppID: "227300"},             // https://store.steampowered.com/app/227300/Euro_Truck_Simulator_2/
	"Factorio":                               {SteamAppID: "427520"},             // https://store.steampowered.com/app/427520/Factorio/
	"Fistful of Frags":                       {SteamAppID: "265630"},             // https://store.steampowered.com/app/265630/Fistful_of_Frags/
	"Frozen Flame":                           {SteamAppID: "715400"},             // https://store.steampowered.com/app/715400/Frozen_Flame/
	"Garry's Mod":                            {SteamAppID: "4000"},               // https://store.steampowered.com/app/4000/Garrys_Mod/
	"Grand Theft Auto IV":                    {SteamAppID: "12210"},              // https://store.steampowered.com/app/12210/Grand_Theft_Auto_IV_The_Complete_Edition/
	"Grand Theft Auto V":                     {SteamAppID: "271590"},             // https://store.steampowered.com/app/271590/Grand_Theft_Auto_V/?l=french
	"Grand Theft Auto: San Andreas":          {SteamAppID: "12120"},              // https://steamdb.info/app/12120/ (original GTA:SA store app, retired from sale; store URL https://store.steampowered.com/app/12120/Grand_Theft_Auto_San_Andreas/ also seen)
	"Half-Life":                              {SteamAppID: "70"},                 // https://store.steampowered.com/app/70/HalfLife/
	"Half-Life 2: Deathmatch":                {SteamAppID: "320"},                // https://store.steampowered.com/app/320/HalfLife_2_Deathmatch/
	"Half-Life Deathmatch: Source":           {SteamAppID: "360"},                // https://store.steampowered.com/app/360/HalfLife_Deathmatch_Source/
	"Half-Life: Opposing Force":              {SteamAppID: "50"},                 // https://store.steampowered.com/app/50/HalfLife_Opposing_Force/
	"HumanitZ":                               {SteamAppID: "1766060"},            // https://store.steampowered.com/app/1766060/HumanitZ/
	"Hurtworld":                              {SteamAppID: "393420"},             // https://store.steampowered.com/app/393420/Hurtworld/
	"HYPERCHARGE: Unboxed":                   {SteamAppID: "523660"},             // https://store.steampowered.com/app/523660/HYPERCHARGE_Unboxed/
	"Insurgency":                             {SteamAppID: "222880"},             // https://store.steampowered.com/app/222880/Insurgency/
	"Insurgency: Sandstorm":                  {SteamAppID: "581320"},             // https://store.steampowered.com/app/581320/Insurgency_Sandstorm/
	"IOSoccer":                               {SteamAppID: "673560"},             // https://store.steampowered.com/app/673560/IOSoccer/
	"Jabroni Brawl: Episode 3":               {SteamAppID: "869480"},             // https://store.steampowered.com/app/869480/Jabroni_Brawl_Episode_3/
	"Just Cause 2":                           {SteamAppID: "8190"},               // https://store.steampowered.com/app/8190/Just_Cause_2/
	"Just Cause 3":                           {SteamAppID: "225540"},             // https://store.steampowered.com/app/225540/Just_Cause_3/
	"Killing Floor":                          {SteamAppID: "1250"},               // https://store.steampowered.com/app/1250/Killing_Floor/
	"Killing Floor 2":                        {SteamAppID: "232090"},             // https://store.steampowered.com/app/232090/Killing_Floor_2/
	"Left 4 Dead":                            {SteamAppID: "500"},                // https://store.steampowered.com/app/500/Left_4_Dead/
	"Left 4 Dead 2":                          {SteamAppID: "550"},                // https://store.steampowered.com/app/550/Left_4_Dead_2/
	"Military Conflict: Vietnam":             {SteamAppID: "1012110"},            // https://store.steampowered.com/app/1012110/Military_Conflict_Vietnam/
	"Mindustry":                              {SteamAppID: "1127400"},            // https://store.steampowered.com/app/1127400/Mindustry/
	"Modiverse":                              {SteamAppID: "1281150"},            // https://store.steampowered.com/app/1281150/Modiverse/
	"MORDHAU":                                {SteamAppID: "629760"},             // https://store.steampowered.com/app/629760/MORDHAU/
	"Natural Selection 2":                    {SteamAppID: "4920"},               // https://store.steampowered.com/app/4920/Natural_Selection_2/
	"Necesse":                                {SteamAppID: "1169040"},            // https://store.steampowered.com/app/1169040/Necesse/
	"Neverwinter Nights: Enhanced Edition":   {SteamAppID: "704450"},             // https://store.steampowered.com/app/704450/Neverwinter_Nights_Enhanced_Edition/
	"Nightingale":                            {SteamAppID: "1928980"},            // https://store.steampowered.com/app/1928980/Nightingale/
	"No More Room in Hell":                   {SteamAppID: "224260"},             // https://store.steampowered.com/app/224260/No_More_Room_in_Hell/
	"Nova-Life: Amboise":                     {SteamAppID: "885570"},             // https://store.steampowered.com/app/885570/NovaLife_Amboise/
	"NS2: Combat":                            {SteamAppID: "310110"},             // https://store.steampowered.com/app/310110/NS2_Combat/
	"Nuclear Dawn":                           {SteamAppID: "17710"},              // https://store.steampowered.com/app/17710/Nuclear_Dawn/
	"OpenArena":                              {GitHub: "OpenArena"},              // Not on Steam; github.com/OpenArena avatar is the OpenArena wordmark logo
	"OpenTTD":                                {SteamAppID: "1536610"},            // https://store.steampowered.com/app/1536610/OpenTTD/
	"Operation: Harsh Doorstop":              {SteamAppID: "736590"},             // https://store.steampowered.com/app/736590/Operation_Harsh_Doorstop/
	"Palworld":                               {SteamAppID: "1623730"},            // https://store.steampowered.com/app/1623730/Palworld/
	"Pavlov VR":                              {SteamAppID: "555160"},             // https://store.steampowered.com/app/555160/Pavlov_VR/
	"Pirates, Vikings, and Knights II":       {SteamAppID: "17570"},              // https://store.steampowered.com/app/17570/Pirates_Vikings__Knights_II/
	"Project CARS":                           {SteamAppID: "234630"},             // https://steamdb.info/app/234630/ (store app retired/delisted but id is the game)
	"Project CARS 2":                         {SteamAppID: "378860"},             // https://store.steampowered.com/app/378860/Project_CARS_2/
	"Project Zomboid":                        {SteamAppID: "108600"},             // https://store.steampowered.com/app/108600/Project_Zomboid/
	"Puck":                                   {SteamAppID: "2994020"},            // https://store.steampowered.com/app/2994020/Puck/
	"QANGA":                                  {SteamAppID: "1648190"},            // https://store.steampowered.com/app/1648190/QANGA/
	"Quake Live":                             {SteamAppID: "282440"},             // https://store.steampowered.com/app/282440/Quake_Live/
	"Red Alert":                              {SteamAppID: "2229840"},            // https://store.steampowered.com/app/2229840/Command__Conquer_Red_Alert_Counterstrike_and_The_Aftermath/
	"Red Dead Redemption 2":                  {SteamAppID: "1174180"},            // https://store.steampowered.com/app/1174180/Red_Dead_Redemption_2/
	"Red Eclipse":                            {SteamAppID: "967460"},             // https://store.steampowered.com/app/967460/Red_Eclipse/
	"Red Orchestra: Ostfront 41-45":          {SteamAppID: "1200"},               // https://store.steampowered.com/app/1200/Red_Orchestra_Ostfront_4145/
	"Ricochet":                               {SteamAppID: "60"},                 // https://store.steampowered.com/app/60/Ricochet/
	"RimWorld":                               {SteamAppID: "294100"},             // https://store.steampowered.com/app/294100/RimWorld/
	"Rising World":                           {SteamAppID: "324080"},             // https://store.steampowered.com/app/324080/Rising_World/
	"RollerCoaster Tycoon 2":                 {SteamAppID: "285330"},             // https://store.steampowered.com/app/285330/RollerCoaster_Tycoon_2_Triple_Thrill_Pack/
	"RuneScape: Dragonwilds":                 {SteamAppID: "1374490"},            // https://store.steampowered.com/app/1374490/RuneScape_Dragonwilds/
	"Rust":                                   {SteamAppID: "252490"},             // https://store.steampowered.com/app/252490/Rust/
	"Satisfactory":                           {SteamAppID: "526870"},             // https://store.steampowered.com/app/526870/Satisfactory/
	"SCP: Secret Laboratory":                 {SteamAppID: "700330"},             // https://store.steampowered.com/app/700330/SCP_Secret_Laboratory/
	"Smalland: Survive the Wilds":            {SteamAppID: "768200"},             // https://store.steampowered.com/app/768200/Smalland_Survive_the_Wilds/
	"Solace Crafting":                        {SteamAppID: "670260"},             // https://store.steampowered.com/app/670260/Solace_Crafting/
	"Soldat":                                 {SteamAppID: "638490"},             // https://store.steampowered.com/app/638490/Soldat/
	"Soulmask":                               {SteamAppID: "2646460"},            // https://store.steampowered.com/app/2646460/Soulmask/
	"Squad":                                  {SteamAppID: "393380"},             // https://store.steampowered.com/app/393380/Squad/
	"Squad 44":                               {SteamAppID: "736220"},             // https://store.steampowered.com/app/736220/Squad_44/
	"Star Wars Jedi Knight II: Jedi Outcast": {SteamAppID: "6030"},               // https://store.steampowered.com/app/6030/STAR_WARS_Jedi_Knight_II__Jedi_Outcast/
	"Star Wars Jedi Knight: Jedi Academy":    {SteamAppID: "6020"},               // https://store.steampowered.com/app/6020/STAR_WARS_Jedi_Knight__Jedi_Academy/
	"Starbound":                              {SteamAppID: "211820"},             // https://store.steampowered.com/app/211820/Starbound/
	"Stationeers":                            {SteamAppID: "544550"},             // https://store.steampowered.com/app/544550/Stationeers/
	"StickyBots":                             {SteamAppID: "889400"},             // https://store.steampowered.com/app/889400/StickyBots/
	"SuperTuxKart":                           {GitHub: "supertuxkart"},           // Not on Steam store (Greenlight only); github.com/supertuxkart avatar shows Tux in a red kart, the STK mascot art
	"Survive the Nights":                     {SteamAppID: "541300"},             // https://store.steampowered.com/app/541300/Survive_the_Nights/
	"Sven Co-op":                             {SteamAppID: "225840"},             // https://store.steampowered.com/app/225840/Sven_Coop/
	"Team Fortress 2":                        {SteamAppID: "440"},                // https://store.steampowered.com/app/440/Team_Fortress_2/
	"Team Fortress 2 Classified":             {SteamAppID: "3545060"},            // https://store.steampowered.com/app/3545060/Team_Fortress_2_Classified/
	"Team Fortress Classic":                  {SteamAppID: "20"},                 // https://store.steampowered.com/app/20/Team_Fortress_Classic/
	"Teeworlds":                              {SteamAppID: "380840"},             // https://store.steampowered.com/app/380840/Teeworlds/
	"Terraria":                               {SteamAppID: "105600"},             // https://store.steampowered.com/app/105600/Terraria/
	"The Bus":                                {SteamAppID: "491540"},             // https://store.steampowered.com/app/491540/The_Bus/
	"The Front":                              {SteamAppID: "2285150"},            // https://store.steampowered.com/app/2285150/The_Front/
	"The Isle":                               {SteamAppID: "376210"},             // https://store.steampowered.com/app/376210/The_Isle/
	"Tiberian Dawn":                          {SteamAppID: "2229830"},            // https://store.steampowered.com/app/2229830/Command__Conquer_and_The_Covert_Operations/
	"Tower Unite":                            {SteamAppID: "394690"},             // https://store.steampowered.com/app/394690/Tower_Unite/
	"Unciv":                                  {SteamAppID: "2118950"},            // https://steamdb.info/app/2118950/ (Unciv store app retired but id is the game)
	"Unturned":                               {SteamAppID: "304930"},             // https://store.steampowered.com/app/304930/Unturned/
	"Valheim":                                {SteamAppID: "892970"},             // https://store.steampowered.com/app/892970/Valheim/
	"VEIN":                                   {SteamAppID: "1857950"},            // https://store.steampowered.com/app/1857950/VEIN/
	"Veloren":                                {GitHub: "veloren"},                // Not on Steam; github.com/veloren (official mirror org) avatar is the Veloren V logo
	"Vintage Story":                          {GitHub: "anegostudios"},           // Not on Steam; github.com/anegostudios (Vintage Story developer) avatar is the Anego Studios tree emblem
	"Warfork":                                {SteamAppID: "671610"},             // https://store.steampowered.com/app/671610/Warfork/
	"Wolfenstein: Enemy Territory":           {SteamAppID: "1873030"},            // https://store.steampowered.com/app/1873030/Wolfenstein_Enemy_Territory/
	"World of PADMAN":                        {GitHub: "PadWorld-Entertainment"}, // Not on Steam store; github.com/PadWorld-Entertainment (owner of worldofpadman repo) avatar is the PadWorld Entertainment logo
	"Wurm Unlimited":                         {SteamAppID: "366220"},             // https://store.steampowered.com/app/366220/Wurm_Unlimited/
	"Xonotic":                                {GitHub: "xonotic"},                // Not on Steam; github.com/xonotic avatar is the Xonotic emblem (orange bird in blue ring)
	"Zombie Panic! Source":                   {SteamAppID: "17500"},              // https://store.steampowered.com/app/17500/Zombie_Panic_Source/
}

// No reliable artwork source, so the UI shows a monogram:
//   Cube 2: Sauerbraten: Not on Steam; no official GitHub org found (project hosted on SourceForge)
//   Dune 2000: OpenRA avatar is the Red Alert mark, misleading for Dune 2000
//   Minecraft: Bedrock Edition: Not on Steam; linked repos are third-party server projects (Allay/PowerNukkitX/WaterdogPE), not Mojang
//   Minecraft: Java Edition: Not sold on Steam; official GitHub org 'Mojang' avatar is the default octocat placeholder
