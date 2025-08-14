#!/bin/bash

# Run each replica using go run . (runs main package)
osascript -e 'tell app "Terminal" to do script "cd \"/Users/whoismahd1/GolandProjects/epaxos\" && go run . --id=0 --log-level=debug"'
osascript -e 'tell app "Terminal" to do script "cd \"/Users/whoismahd1/GolandProjects/epaxos\" && go run . --id=1 --log-level=debug"'
osascript -e 'tell app "Terminal" to do script "cd \"/Users/whoismahd1/GolandProjects/epaxos\" && go run . --id=2 --log-level=debug"'
osascript -e 'tell app "Terminal" to do script "cd \"/Users/whoismahd1/GolandProjects/epaxos\" && go run . --id=3 --log-level=debug"'
osascript -e 'tell app "Terminal" to do script "cd \"/Users/whoismahd1/GolandProjects/epaxos\" && go run . --id=4 --log-level=debug"'