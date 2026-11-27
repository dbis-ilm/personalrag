package i18n

type entry struct{ de, en string }

// catalog holds the Go-side texts. Arguments follow fmt verbs and must be
// the same in both languages (TestCatalog checks this).
var catalog = map[string]entry{
	// Tray menu and dialogs
	"tray.open":        {"PersonalRAG öffnen", "Open PersonalRAG"},
	"tray.pause":       {"Indexierung pausieren", "Pause indexing"},
	"tray.quit":        {"Beenden", "Quit"},
	"dialog.addFolder": {"Ordner indexieren", "Index a folder"},
	"dialog.add":       {"Hinzufügen", "Add"},

	// Core and index
	"core.keychain":       {"Schlüsselbund: %w", "Keychain: %w"},
	"core.rebuilding":     {"Der Index wird für ein neues Embedding-Modell neu aufgebaut; bitte PersonalRAG öffnen", "The index is being rebuilt for a new embedding model; please open PersonalRAG"},
	"core.openIndex":      {"Index öffnen: %w", "Opening the index: %w"},
	"core.notReady":       {"Der Index ist noch nicht bereit", "The index is not ready yet"},
	"core.noEmbedding":    {"Kein Embedding-Modell eingerichtet", "No embedding model set up"},
	"core.noAnswerModel":  {"Kein Antwortmodell eingerichtet – Suchmodus verwenden", "No answer model set up – use search mode"},
	"core.noLog":          {"Kein Protokoll eingerichtet", "No log set up"},
	"core.tooNew":         {"Der Index stammt von einer neueren PersonalRAG-Version. Bitte die neueste Version installieren", "The index was written by a newer version of PersonalRAG. Please install the latest version"},
	"core.consentMissing": {"Freigabe fehlt: Textauszüge würden an %s gesendet. Bitte die Modelle in den Einstellungen erneut bestätigen", "Consent missing: excerpts would be sent to %s. Please confirm the models again in Settings"},

	// Folders and documents
	"folder.notADir":        {"%s ist kein Ordner", "%s is not a folder"},
	"folder.inCollection":   {"Der Ordner gehört bereits zur Sammlung „%s“", "The folder already belongs to the collection “%s”"},
	"folder.contains":       {"Der Ordner enthält %s aus der Sammlung „%s“. Entferne diese Sammlung zuerst oder wähle einen anderen Ordner", "The folder contains %s from the collection “%s”. Remove that collection first or choose another folder"},
	"doc.notIndexed":        {"Das Dokument ist nicht im Index", "The document is not in the index"},
	"drop.foldersAdded":     {"%d Ordner hinzugefügt", "%d folder(s) added"},
	"drop.filesQueued":      {"%d Datei(en) werden zuerst indexiert", "%d file(s) will be indexed first"},
	"drop.filesOutside":     {"%d Datei(en) liegen in keinem überwachten Ordner, bitte den Ordner hinzufügen", "%d file(s) are not in a watched folder; please add the folder"},
	"drop.filesUnsupported": {"%d Datei(en) im nicht unterstützten Format", "%d file(s) in an unsupported format"},
	"exclusions.scanFailed": {"Abgleich nach Änderung der Ausschlüsse fehlgeschlagen: %s", "Re-scan after changing the exclusions failed: %s"},
	"exclusions.notName":    {"%q: nur Datei- oder Ordnernamen, keine Pfade", "%q: file or folder names only, no paths"},
	"exclusions.invalid":    {"%q ist kein gültiges Muster", "%q is not a valid pattern"},
	"exclusions.hidden":     {"Versteckte Dateien und Ordner (.*)", "Hidden files and folders (.*)"},
	"exclusions.dev":        {"node_modules, __pycache__", "node_modules, __pycache__"},
	"exclusions.office":     {"Office-Sperrdateien (~$*) und *.tmp", "Office lock files (~$*) and *.tmp"},
	"exclusions.size":       {"Dateien über 200 MB", "Files over 200 MB"},

	// Asking
	"search.keywordOnly":  {"Nur Volltextsuche: Das Embedding-Modell ist nicht erreichbar.", "Keyword search only: the embedding model is not reachable."},
	"search.expired":      {"Die Suche ist abgelaufen, bitte erneut fragen", "The search has expired, please ask again"},
	"answer.failed":       {"Die Antwort konnte nicht erzeugt werden: %s", "The answer could not be generated: %s"},
	"answer.nothingFound": {"In deinen Dokumenten habe ich dazu nichts gefunden.", "I found nothing about this in your documents."},
	"answer.noHits":       {"keine Treffer", "no matches"},
	"answer.fallback":     {"keine Quelle eindeutig relevant", "no source clearly relevant"},
	"answer.used":         {"%d von %d Treffern als relevant bewertet", "%d of %d matches rated relevant"},
	"answer.interrupted":  {"Antwort wurde nicht fertiggestellt (App beendet).", "The answer was not finished (the app was closed)."},

	// Connections and models
	"conn.ollamaUnreachable": {"Ollama ist unter %s nicht erreichbar.", "Ollama is not reachable at %s."},
	"conn.gpustackError":     {"GPUStack unter %s: %s", "GPUStack at %s: %s"},
	"conn.ollama":            {"Ollama: %w", "Ollama: %w"},
	"conn.gpustack":          {"GPUStack: %w", "GPUStack: %w"},
	"conn.gpustackInUse":     {"Ein gewähltes Modell läuft über GPUStack. Wähle zuerst andere Modelle", "A selected model runs on GPUStack. Choose other models first"},
	"conn.gpustackMissing":   {"GPUStack ist nicht eingerichtet", "GPUStack is not set up"},
	"conn.providerUnknown":   {"Der Anbieter von %q wird nicht unterstützt", "The provider of %q is not supported"},
	"conn.embeddingRequired": {"Ein Embedding-Modell ist erforderlich", "An embedding model is required"},
	"conn.embeddingFailed":   {"Embedding-Modell %s: %s", "Embedding model %s: %s"},
	"conn.consentRequired":   {"Bestätigung nötig: Mit diesem Modell werden Textauszüge deiner Dokumente an einen anderen Rechner gesendet", "Confirmation needed: with this model, excerpts of your documents are sent to another machine"},
	"conn.badURL":            {"%q ist keine gültige Adresse (http:// oder https:// mit Host)", "%q is not a valid address (http:// or https:// with a host)"},
	"conn.credentialsInURL":  {"Zugangsdaten gehören nicht in die Adresse, bitte das Feld für den API-Schlüssel nutzen", "Credentials do not belong in the address; please use the API key field"},
	"conn.notEmbedding":      {"%s ist kein Embedding-Modell oder nicht verfügbar", "%s is not an embedding model or not available"},
	"conn.noVector":          {"%s lieferte keinen Vektor", "%s returned no vector"},
	"conn.keyRejected":       {"API-Schlüssel abgelehnt", "API key rejected"},
	"conn.serverStatus":      {"Server antwortet mit %s", "Server answered with %s"},
	"conn.unreachable":       {"nicht erreichbar", "not reachable"},

	// Agents view: how each client is set up
	"client.opencode":      {"In opencode.json im Projekt oder in ~/.config/opencode/opencode.json eintragen.", "Add to opencode.json in the project or to ~/.config/opencode/opencode.json."},
	"client.claudeDesktop": {"In claude_desktop_config.json eintragen und Claude Desktop neu starten.", "Add to claude_desktop_config.json and restart Claude Desktop."},
	"client.claudeCode":    {"Im Terminal ausführen.", "Run in a terminal."},
	"client.cursor":        {"In ~/.cursor/mcp.json eintragen.", "Add to ~/.cursor/mcp.json."},
}
