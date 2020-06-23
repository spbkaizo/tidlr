# tidlr
Golang based Tidal FLAC/MQA Downloader

I wrote this utility as my DAC doesn't support the Offline Files option (A&K AK70 MkII) which is hugely irritating, as effectively I can't use Tidal on the move to full effect.  Morally, you're on your own with how you use this utility, don't abuse Tidal's ability to support offline files.

# Changelog

### 0.9.4

* Added in online update of tokens

### 0.9.3

* Tidal changed the access tokens, so they have been rotated in the app.
* There's a JSON file available at https://cdn.jsdelivr.net/gh/yaronzz/Tidal-Media-Downloader@latest/Else/tokens.json which seems to keep track of new tokens
* TODO: Implement JSON download of tokens file at runtime to remove need for manual recompile, with fall back on pre-defined tokens.

### 0.9.2

* You can now paste a full URL for a playlist to the command line, and the tool will strip out the playlist, making things slightly easier for using the "Copy Link" option in Tidal

### 0.9.1

* Added new functionality to download playlists into a folder named after the playlist
* Playlist Download also sets the track numbers to be synomynous with the original order in playlist
* Playlist also now sets the "Album" metatag to be the same for all files in a playlist, based on the list name
* Artist Name is set to "Tidal" for all playlists now
