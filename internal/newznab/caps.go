package newznab

import "fmt"

func capsXML() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.0" title="iplayer-arr" />
  <limits max="100" default="50" />
  <searching>
    <search available="yes" supportedParams="q" />
    <tv-search available="yes" supportedParams="q,season,ep,tvdbid" />
    <movie-search available="yes" supportedParams="q,year" />
    <audio-search available="no" supportedParams="" />
  </searching>
  <categories>
    <category id="5000" name="TV">
      <subcat id="5045" name="TV/UHD" />
      <subcat id="5040" name="TV/HD" />
      <subcat id="5030" name="TV/SD" />
    </category>
    <category id="2000" name="Movies">
      <subcat id="2045" name="Movies/UHD" />
      <subcat id="2040" name="Movies/HD" />
      <subcat id="2030" name="Movies/SD" />
    </category>
  </categories>
</caps>`)
}
