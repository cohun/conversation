\# English Teacher & Conversational Companion – Antigravity Master Specifikáció

Ez a dokumentum az Antigravity kódgeneráló és projektépítő környezet számára készült, amely alapján az önálló, mobilra optimalizált Go \+ SQLite \+ Gemini Live API webalkalmazás egyetlen utasításra teljes egészében felépíthető és Google Cloud Run-ra telepíthető.

\---

\#\# 1\. Projekt Áttekintés és Felhasználói Élmény (UX)

\#\#\# 1.1 Fő Célkitűzés  
Egy elegáns, mobiltelefonra optimalizált, tiszta hangalapú webalkalmazás, amely szimulált \*\*telefonhívásként\*\* kapcsolja össze a felhasználót különböző mesterséges intelligencia személyiségekkel (Personas).   
Az alapértelmezett partner egy rendkívül barátságos, türelmes és támogató \*\*angol tanár\*\*, akivel valós időben, tiszta élőszóban lehet társalogni.

\#\#\# 1.2 Kulcsfontosságú UX Alapelvek  
\- \*\*Tiszta hangélmény (Voice-first / Screen-text free):\*\* A hívás közben nincs képernyőn futó csevegési szöveg vagy felirat. A kommunikáció 100%-ban élőszóban történik.  
\- \*\*Telefonhívás metafora (Call Interface):\*\* A beszélgetés indítása egy telefonhíváshoz hasonlít: hívásindítás, aktív időtartamszámláló, finoman pulzáló állapotjelző (beszél / hallgat), némítás és hívásbontás.  
\- \*\*Név és kiejtési ceremónia (Name Handshake):\*\* A hívás indításakor (vagy a profilban) a felhasználó megadja írásban a nevét, és a mikrofonba ki is mondja azt. A tanár ezt megkapja közvetlen kontextusként, így a megszólításakor garantáltan a helyes magyar/angol kiejtéssel és hangsúllyal ejti ki a felhasználó nevét.  
\- \*\*Zavarmentes beszéd és Turn-taking:\*\*  
  \- A tanár rendkívül türelmes: megvárja, amíg a felhasználó teljesen befejezi a gondolatát, nem vág közbe a mondatközi szüneteknél vagy hezitálásoknál.  
  \- A szót át lehet adni szóbeli formulával (pl. "What do you think?", "Your turn") VAGY a képernyőn lévő "Átadom a szót" gombbal.  
\- \*\*Barge-in (Azonnali közbevágás):\*\* Ha a felhasználó bármikor beleszól a tanár beszédébe, a rendszer azonnal elnémítja a kimenő hangot és átvált hallgatási állapotba.  
\- \*\*Kellemes, nyugodt vizuális világ:\*\* Pasztell, letisztult színek (lágy homok, zsályazöld, meleg szürke), modern tipográfia, finom rajzolt illusztrációk és letisztult SVG ikonok.

\---

\#\# 2\. Pedagógiai és Beszélgetési Dinamika (Prompt Architektúra)

\#\#\# 2.1 Az Alapértelmezett Tanár (Default Teacher Persona) 3 Fázisú Ciklusa  
Amikor a tanár megkapja a szót, kötelezően az alábbi három fázist követi:  
1\. \*\*Fázis 1 – Hibajavítás és helyes ismétlés:\*\*  
   \- Kedvesen és röviden szóban rámutat a nyelvtani, kifejezésbeli vagy kiejtési hibákra.  
   \- Elismétli a teljes mondatot tiszta, tökéletes és természetes angolsággal.  
2\. \*\*Fázis 2 – Magyarázat és felajánlott begyakoroltatás:\*\*  
   \- Röviden felajánlja a nyelvtani szabály elmagyarázását és a frázis begyakorlását (drill).  
   \- Ha a felhasználó gyakorolni szeretne, megdicséri és kijavítja. Ha a felhasználó jelzi, hogy lépjenek tovább, azonnal a 3\. fázisra lép.  
3\. \*\*Fázis 3 – Érdemi válasz és beszélgetés vitele:\*\*  
   \- Érdemben reagál a felhasználó gondolataira, saját véleményt formál, és nyitott kérdéssel vagy új gondolattal ösztönzi a beszélgetés folytatását.

\#\#\# 2.2 Egyéb Személyiségek (Personas)  
\- Minden persona rendelkezik saját névvel, szerepleírással, kiválasztott Live hangszínnel (Voice) és működési móddal:  
  \- \*\*Pedagógiai mód (Teacher mode):\*\* Követi a fenti 3 fázisú oktatási protokollt.  
  \- \*\*Szabad társalgási mód (Chat mode):\*\* Nem javít és nem oktat; egyenrangú partnerként, a megadott személyiség jegyei szerint beszélget.

\---

\#\# 3\. Memória és Témamentés (Save & Resume)

\#\#\# 3.1 Mentési Logika (Function Calling / UI gomb)  
A beszélgetés bármikor elmenthető:  
\- \*\*Szóban:\*\* A felhasználó kimondja: "Let's save this topic for later" (vagy hasonlót). Az AI felismeri ezt és meghívja a \`save\_conversation\` tool-t.  
\- \*\*Képernyőn:\*\* A felhasználó megérinti a "Téma mentése / Könyvjelző" gombot.  
\- \*\*Mentett tartalom:\*\*  
  \- \`topic\_title\`: Rövid, kifejező cím.  
  \- \`summary\`: 2-3 mondatos lényegi összefoglaló az elhangzottakról.  
  \- \`key\_points\`: Kulcsfontosságú fogalmak vagy új szavak listája.  
  \- \`open\_question\`: Az utolsó megválaszolatlan kérdés vagy felvetés.  
\- A modell a mentés után szóban visszajelez: "I've saved our conversation about \[Topic\]. We can pick it up anytime\!"

\#\#\# 3.2 Folytatás (Resume Call)  
\- A felületen a "Mentett témák" listából a felhasználó egyetlen érintéssel hívást indíthat.  
\- A Go backend a munkamenet inicializálásakor betölti az előzményeket az AI felé:  
  \- \*"We are resuming a previous conversation with user \[Name\]. Previous context: \[summary\]. Key points: \[key\_points\]. Open question: \[open\_question\]. Greet the user warmly and reference this context to kick off the dialogue."\*

\---

\#\# 4\. Rendszerarchitektúra és Technológiai Stack

\#\#\# 4.1 Backend: Go (Golang)  
\- \*\*Felelősség:\*\*  
  \- HTTP webszerver és statikus fájlok kiszolgálása.  
  \- SQLite adatbázis-kezelés CGO-mentes pure Go meghajtóval (\`modernc.org/sqlite\`).  
  \- WebSocket proxy: Kétirányú valós idejű audio stream közvetítése a böngésző és a Google Gemini Multimodal Live API között.  
  \- Dinamikus Modellfeloldó és Fallback motor (\`internal/gemini/resolver.go\`).  
  \- Gemini Live Session setup: System instructions, Voice konfiguráció, \`save\_conversation\` tool kezelése.  
  \- Barge-in eseménykezelés: interrupt jelzés azonnali átadása.

\#\#\# 4.2 Hosting Célplatform: Google Cloud Run  
\- \*\*Optimalizáció:\*\*  
  \- \`PORT\` környezeti változó figyelése (alapértelmezett: 8080).  
  \- Hosszú életű WebSocket kapcsolatok támogatása (\`--timeout=3600\`, akár 60 perces hívások).  
  \- Konténerizált több-fázisú Dockerfile (minimális Alpine/scratch futtatókörnyezet).

\#\#\# 4.3 Multi-User Felkészítés és SQLite Adatmodell  
Minden lényeges táblában szerepel a \`user\_id\` mező (alapértelmezett: \`'default\_user'\`). Így most azonnal egyetlen felhasználóval működik, de később egyszerűen több-felhasználóssá alakítható kódmódosítás nélkül.

\- \*\*\`user\_profile\` tábla:\*\*  
  \- \`id\` INTEGER PRIMARY KEY AUTOINCREMENT  
  \- \`user\_id\` TEXT NOT NULL DEFAULT 'default\_user'  
  \- \`display\_name\` TEXT NOT NULL  
  \- \`phonetic\_note\` TEXT  
  \- \`created\_at\` DATETIME DEFAULT CURRENT\_TIMESTAMP  
\- \*\*\`personas\` tábla:\*\*  
  \- \`id\` TEXT PRIMARY KEY  
  \- \`user\_id\` TEXT NOT NULL DEFAULT 'default\_user'  
  \- \`name\` TEXT NOT NULL  
  \- \`short\_desc\` TEXT NOT NULL  
  \- \`system\_prompt\` TEXT NOT NULL  
  \- \`voice\_name\` TEXT NOT NULL (pl. Aoede, Puck, Charon, Fenrir, Kore)  
  \- \`mode\` TEXT NOT NULL CHECK(mode IN ('teacher', 'free\_chat'))  
  \- \`is\_default\` BOOLEAN DEFAULT 0  
  \- \`avatar\_icon\` TEXT  
  \- \`created\_at\` DATETIME DEFAULT CURRENT\_TIMESTAMP  
\- \*\*\`saved\_conversations\` tábla:\*\*  
  \- \`id\` TEXT PRIMARY KEY  
  \- \`user\_id\` TEXT NOT NULL DEFAULT 'default\_user'  
  \- \`persona\_id\` TEXT NOT NULL  
  \- \`topic\_title\` TEXT NOT NULL  
  \- \`summary\` TEXT NOT NULL  
  \- \`key\_points\` TEXT  
  \- \`open\_question\` TEXT  
  \- \`created\_at\` DATETIME DEFAULT CURRENT\_TIMESTAMP  
  \- \`last\_resumed\_at\` DATETIME  
  \- FOREIGN KEY(persona\_id) REFERENCES personas(id)  
\- \*\*\`app\_settings\` tábla:\*\*  
  \- \`key\` TEXT PRIMARY KEY  
  \- \`value\` TEXT NOT NULL  
  \- Alapértelmezett: \`('default\_live\_model', 'auto')\`

\#\#\# 4.4 AI Hangkapcsolat és Dinamikus Modellválasztás (Always-Optimal Resolution)  
\- \*\*Protokoll:\*\* Kétirányú WebSocket audio stream a Gemini Live API felé.  
\- \*\*Bemenő audio:\*\* 16kHz PCM mono audio a böngészőből.  
\- \*\*Kimenő audio:\*\* 24kHz PCM audio a modelltől, közvetlen valós idejű lejátszás Web Audio API-val.  
\- \*\*Dinamikus feloldási logika:\*\*  
  1\. Explicit felülbírálás: \`GEMINI\_LIVE\_MODEL\` környezeti változó vagy \`app\_settings\` érték alapján.  
  2\. Auto-discovery: Ha 'auto', a Go szerver induláskor lekéri az elérhető modelleket a Gemini API-tól, és kiszűri azokat, amelyek támogatják a \`bidiGenerateContent\` metódust.  
  3\. Verziórangsorolás: A legújabb és legstabilabb kiadást választja.  
  4\. Hibamentes failover: Ha a választott modellre indított kapcsolat 404 vagy Deprecated státuszt ad, a proxy azonnal a következő elérhető stabil modellre vált át a hívás megszakadása nélkül.

\#\#\# 4.5 Mobilra Optimalizált WebApp Frontend  
\- \*\*Web Audio API integráció:\*\*  
  \- Mikrofon rögzítése és PCM konverzió (AudioWorklet).  
  \- Visszajátszó puffer kezelése, azonnali flush közbevágás (barge-in) esetén.  
\- \*\*Képernyők:\*\*  
  1\. \*\*Főképernyő / Névjegytár:\*\* Personák kártyái portréval, leírással, hívás gombbal; Mentett témák fül resume gombbal; Profilbeállítások.  
  2\. \*\*Aktív Hívás Képernyő:\*\* Fullscreen, pulzáló gyűrű/hullámforma animáció (Listening / Speaking), hívásidő, Mute gomb, "Átadom a szót" gomb, Könyvjelző gomb, Piros hívásbontás.  
  3\. \*\*Új Persona Modál:\*\* Név, leírás, prompt, hangválasztó, üzemmód.  
