\# Antigravity Task Prompts – Angol Tanár WebApp Kódgenerálás

Ez a dokumentum a közvetlenül Antigravity-be illeszthető feladat-utasításokat tartalmazza. 

Ha az Antigravity-t a teljes projekt egy lépésben történő megépítésére utasítod, add át a \*\*Mester Promptot\*\*. Ha lépésről lépésre, iteratívan szeretnéd felépíteni és tesztelni az egyes modulokat, használd az \*\*1–4. Részletes Lépéseket\*\*.

\---

\#\# Mester Prompt (Egyetlen utasítással történő teljes generáláshoz)

\`\`\`markdown  
Olvasd el figyelmesen a projekt gyökérkönyvtárában található \`Antigravity\_English\_Teacher\_App\_Spec.md\` specifikációs dokumentumot, és építsd fel a teljes, működőképes webalkalmazást\!

Főbb követelmények:  
1\. Backend: Tiszta Go (Golang 1.22+) webszerver, CGO-mentes pure Go SQLite meghajtóval (\`modernc.org/sqlite\`).  
2\. Adatmodell: Minden lényeges táblában (\`user\_profile\`, \`personas\`, \`saved\_conversations\`) szerepeljen a \`user\_id TEXT NOT NULL DEFAULT 'default\_user'\` mező a jövőbeli multi-user támogatáshoz. Hozd létre az \`app\_settings\` táblát is.  
3\. Alapértelmezett Persona: Sarah, az angol tanár a specifikációban részletezett 3 fázisú pedagógiai ciklussal (hibajavítás & ismétlés \-\> szabálymagyarázat & begyakorlás felajánlása \-\> érdemi válasz és téma folytatása).  
4\. Dinamikus Modellfeloldás: Készítsd el az \`internal/gemini/resolver.go\` modult. Ne hardcode-olj modellnevet: a szerver induláskor keresse meg a legfrissebb elérhető Gemini Live modellt, amely támogatja a \`bidiGenerateContent\` metódust, és kezeljen automatikus tartalékláncot (fallback) hiba esetén.  
5\. Gemini Live API WebSocket Proxy: Kétirányú valós idejű audio stream közvetítése a böngésző és a Google Gemini Live API között, támogatva a közbevágást (barge-in) és a témamentést (\`save\_conversation\` tool hívás).  
6\. Frontend: Mobiltelefonra optimalizált, elegáns, lágy pasztell színvilágú webes felület (HTML5/CSS3/Vanilla JS). Tiszta telefonhívás metafora, képernyőn futó csevegési szöveg nélkül. AudioWorklet 16kHz mikrofon rögzítéshez és 24kHz alacsony késleltetésű lejátszás Web Audio API-val.  
7\. Hosting és Konténerizáció: Készíts egy multi-stage \`Dockerfile\`-t Google Cloud Run-ra optimalizálva (PORT változó figyelése, \`--timeout=3600\` támogatása, \`/data\` könyvtár az SQLite adatbázisnak).

Készítsd el a teljes, azonnal futtatható kódbázist\!  
\`\`\`

\---

\#\# 1\. Részletes Lépés: Go Backend és Multi-User Kész SQLite Adatbázis

\`\`\`markdown  
Készítsd el egy önálló, tiszta architektúrájú Go (Golang) webalkalmazás alapjait egy hangalapú angol tanár és társalgó partner (Personas) rendszerhez.

\#\#\# Technológiai követelmények:  
\- Go 1.22+ szabványos könyvtár vagy chi router.  
\- Beágyazott SQLite adatbázis (\`modernc.org/sqlite\` tiszta Go meghajtó CGO függőség nélkül).  
\- Környezeti változók kezelése (\`PORT\`, \`GEMINI\_API\_KEY\`, \`GEMINI\_LIVE\_MODEL\`, \`DB\_PATH\`).

\#\#\# Adatbázis és Multi-user Adatmodell (\`internal/db/\`):  
Hozd létre az SQLite sémát a \`data/app.db\` fájlban:  
1\. \`user\_profile\`:  
   \- id INTEGER PRIMARY KEY AUTOINCREMENT  
   \- user\_id TEXT NOT NULL DEFAULT 'default\_user'  
   \- display\_name TEXT NOT NULL  
   \- phonetic\_note TEXT  
   \- created\_at DATETIME DEFAULT CURRENT\_TIMESTAMP  
2\. \`personas\`:  
   \- id TEXT PRIMARY KEY  
   \- user\_id TEXT NOT NULL DEFAULT 'default\_user'  
   \- name TEXT NOT NULL (pl. "Sarah")  
   \- short\_desc TEXT NOT NULL  
   \- system\_prompt TEXT NOT NULL  
   \- voice\_name TEXT NOT NULL (pl. "Aoede", "Puck", "Charon", "Fenrir", "Kore")  
   \- mode TEXT NOT NULL CHECK(mode IN ('teacher', 'free\_chat'))  
   \- is\_default BOOLEAN DEFAULT 0  
   \- avatar\_icon TEXT  
   \- created\_at DATETIME DEFAULT CURRENT\_TIMESTAMP  
3\. \`saved\_conversations\`:  
   \- id TEXT PRIMARY KEY  
   \- user\_id TEXT NOT NULL DEFAULT 'default\_user'  
   \- persona\_id TEXT NOT NULL  
   \- topic\_title TEXT NOT NULL  
   \- summary TEXT NOT NULL  
   \- key\_points TEXT  
   \- open\_question TEXT  
   \- created\_at DATETIME DEFAULT CURRENT\_TIMESTAMP  
   \- last\_resumed\_at DATETIME  
   \- FOREIGN KEY(persona\_id) REFERENCES personas(id)  
4\. \`app\_settings\`:  
   \- key TEXT PRIMARY KEY  
   \- value TEXT NOT NULL  
   \- Seed érték: ('default\_live\_model', 'auto')

\#\#\# Seed adatok:  
Induláskor, ha üres az adatbázis, hozz létre egy alapértelmezett Tanár personát:  
\- id: "teacher-sarah"  
\- user\_id: "default\_user"  
\- name: "Sarah"  
\- short\_desc: "Patient, supportive English teacher focused on gentle corrections and natural dialogue."  
\- voice\_name: "Aoede"  
\- mode: "teacher"  
\- is\_default: 1  
\- system\_prompt: A specifikáció szerinti 3 fázisú tanári metodika promptja (1. hibajavítás és helyes ismétlés, 2\. magyarázat és begyakorlás felajánlása, 3\. természetes válasz és nyitott kérdés).

\#\#\# REST API Végpontok:  
\- \`GET /api/profile\` és \`POST /api/profile\` (Név és kiejtési beállítás)  
\- \`GET /api/personas\` és \`POST /api/personas\` (Personák lekérése és új hozzáadása)  
\- \`GET /api/conversations\` (Mentett témák listája)  
\- \`POST /api/conversations\` (Téma mentése)  
\- \`DELETE /api/conversations/{id}\` (Mentett téma törlése)

Készítsd el a teljes, forduló Go forráskódot moduláris felépítésben.  
\`\`\`

\---

\#\# 2\. Részletes Lépés: Dinamikus Modellfeloldó és Gemini Live WebSocket Proxy

\`\`\`markdown  
Egészítsd ki a Go alkalmazást egy valós idejű WebSocket proxy-val és a dinamikus modellfeloldó motorral a Google Gemini Live API-hoz.

\#\#\# 1\. Dinamikus Modellfeloldó (\`internal/gemini/resolver.go\`):  
Ne égesd be fixen egyetlen modell nevét sem a forráskódba\!  
\- Ellenőrizd a \`GEMINI\_LIVE\_MODEL\` környezeti változót és az SQLite \`app\_settings\` tábla \`default\_live\_model\` kulcsát.  
\- Ha explicit modellnév van megadva (nem "auto"), használd azt.  
\- Ha az érték "auto" vagy üres:  
  \- A szerver induláskor kérdezze le a Gemini API-tól az elérhető modellek listáját.  
  \- Szűrje ki azokat a modelleket, amelyek támogatják a \`bidiGenerateContent\` metódust.  
  \- Válassza a legfrissebb stabil verziót (pl. gemini-2.0-flash / újabb elérhető).  
\- Kezelj automatikus fallback láncot: ha a WebSocket kapcsolódás során 404 vagy Deprecated hiba érkezik, próbálja meg a következő stabil modellt.

\#\#\# 2\. Kétirányú WebSocket Proxy (\`internal/ws/hub.go\`):  
\- Végpont a böngésző felé: \`GET /ws/live?persona\_id={id}\&resume\_id={id}\`  
\- Setup Message összeállítása:  
  \- Kiválasztott feloldott modell neve.  
  \- GenerationConfig: audio kimenet, a persona által választott hangszín.  
  \- SystemInstruction: A persona system\_prompt-ja, kiegészítve a felhasználó nevével és kiejtési referenciájával.  
  \- Ha \`resume\_id\` meg van adva: a betöltött kontextus (összefoglaló, kulcsszavak, nyitott kérdés) átadása nyitányként.  
  \- Eszközök (Tools): Definiáld a \`save\_conversation\` függvényt paraméterekkel (\`topic\_title\`, \`summary\`, \`key\_points\`, \`open\_question\`).

\#\#\# 3\. Hangfolyam és Eseménykezelés:  
\- Kliens \-\> Gemini: 16kHz PCM audio továbbítása realtimeInput üzenetként.  
\- Gemini \-\> Kliens: 24kHz PCM audio azonnali továbbítása bináris frame-ként.  
\- Barge-in (Közbevágás): Ha a klienstől \`client\_interrupt\` jelzés érkezik, azonnal továbbítsd a Gemini felé.  
\- Tool Call kezelése: Ha a modell meghívja a \`save\_conversation\` eszközt, mentsd az adatokat az SQLite \`saved\_conversations\` táblájába, és küldd vissza a ToolResponse-t.  
\`\`\`

\---

\#\# 3\. Részletes Lépés: Mobilra Optimalizált Telefonhívás Webes Felület (UI & Audio)

\`\`\`markdown  
Készítsd el a mobiltelefonra optimalizált, elegáns, lágy pasztell színvilágú webes felületet (HTML5, modern CSS és Vanilla JS).

\#\#\# Dizájn és Színvilág:  
\- Finom, prémium árnyalatok: lágy homokszín/bézs háttér, zsályazöld, meleg szürke és terrakotta akcentusok.  
\- Letisztult, lekerekített kártyák, rajzolt stílusú SVG avatarok és tiszta ikonok.  
\- Kifejezetten egykezes mobilhasználatra méretezett kezelőszervek.

\#\#\# 1\. Főképernyő:  
\- Fejléc: Cím és Profil gomb (felhasználó neve és kiejtés-módosító).  
\- Fülek:  
  \- Personas: Kártyák a személyiségekről leírással, hangjelzéssel és zöld "Call" telefon ikonnal.  
  \- Saved Topics: Korábbi beszélgetések listája dátummal, címmel és "Resume Call" gombbal.  
\- Új Persona Modál: Név, leírás, prompt, hangválasztó, üzemmód (Tanár / Csevegő).

\#\#\# 2\. Aktív Telefonhívás Képernyő (Call Screen):  
\- Fullscreen hívásfelület.  
\- Fent: Persona portréja, neve és státusza (Connected • 03:12).  
\- Középen: Finoman pulzáló gyűrű/hullámforma animáció (Listening... zöld pulzálás / Speaking... lágy fény).  
\- Nincs képernyőn futó csevegési szöveg\! A fókusz a tiszta hangélményen marad.  
\- Alsó vezérlősáv:  
  \- Mute mikrofon gomb.  
  \- "Átadom a szót" (Done speaking) gomb.  
  \- "Téma mentése" (Bookmark) gomb.  
  \- Nagy piros "End Call" gomb.

\#\#\# 3\. Név és Kiejtési Képernyő (Name Handshake):  
\- Szöveges név bevitele \+ mikrofon gomb a kiejtés kimondásához és rögzítéséhez.

\#\#\# 4\. Web Audio API és WebSocket Kliens (\`web/static/js/audio-call.js\`):  
\- Mikrofon rögzítése 16kHz PCM formátumban AudioWorklet-tel.  
\- 24kHz PCM audio visszajátszás alacsony késleltetéssel.  
\- Barge-in kezelése a kliensen: Ha a felhasználó megszólal a tanár beszéde közben, a kliens azonnal némítsa el a lejátszási puffert és küldjön \`client\_interrupt\` üzenetet a szervernek.  
\`\`\`

\---

\#\# 4\. Részletes Lépés: Dockerfile és Google Cloud Run Telepítés

\`\`\`markdown  
Készítsd el az alkalmazás konténerizációját kifejezetten Google Cloud Run hosztolásra optimalizálva.

1\. \*\*Multi-stage \`Dockerfile\`:\*\*  
   \- Build fázis (\`golang:1.22-alpine\`):  
     \- Függőségek letöltése és a Go bináris statikus fordítása (\`CGO\_ENABLED=0 GOOS=linux GOARCH=amd64\`).  
   \- Futtató fázis (\`alpine:latest\`):  
     \- \`ca-certificates\` és \`tzdata\` telepítése.  
     \- Nem-root felhasználó (\`appuser\`) és \`/data\` könyvtár létrehozása megfelelő jogosultságokkal.  
     \- Bináris és a \`web/static\` frontend fájlok átmásolása.  
     \- \`EXPOSE 8080\`, \`ENTRYPOINT \["/app/server"\]\`.

2\. \*\*Cloud Run Beállítások és Telepítési Szkript (\`deploy.sh\`):\*\*  
   \- Készíts egy egyszerű deployment parancsot:  
     \`\`\`bash  
     gcloud run deploy english-teacher-app \\  
       \--source . \\  
       \--platform managed \\  
       \--region europe-west1 \\  
       \--allow-unauthenticated \\  
       \--timeout 3600 \\  
       \--set-env-vars GEMINI\_API\_KEY="YOUR\_KEY\_HERE",GEMINI\_LIVE\_MODEL="auto"  
     \`\`\`  
\`\`\`  
