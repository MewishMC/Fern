windres -o .\Fern\Fern.syso .\Fern\Fern.rc
go generate .\Fern\
go build -ldflags "-H windowsgui" .\Fern\
go build -ldflags "-H windowsgui" .\update\