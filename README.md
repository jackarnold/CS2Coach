# CS2Coach

An AI-powered Counter-Strike 2 demo analyzer and coaching tool that provides personalized feedback and performance metrics.

## Features

- Demo parsing and analysis
- Advanced player statistics tracking (aim, positioning, utility usage)
- AI-powered coaching recommendations
- Leetify-style metrics and performance scoring
- Machine learning model for player impact prediction

## Installation

```bash
git clone https://github.com/richardkiene/CS2Coach
cd CS2Coach
go mod download
```

## Usage

### Analyze a Demo

```bash
go run cmd/main.go analyze -demo path/to/demo.dem -player "playername"
```

Options:
- `-demo`: Path to CS2 demo file
- `-player`: Player name to analyze
- `-steamid`: Steam ID to analyze (alternative to player name)
- `-debug`: Enable debug output
- `-verbose`: Show detailed metrics

### Train ML Model

```bash
go run cmd/main.go train -demodir path/to/demos -config path/to/config.json
```

### Predict Player Impact

```bash
go run cmd/main.go predict -demo path/to/demo.dem -player "playername"
```

## Performance Metrics

- Combat Performance (ADR, headshot %, accuracy)
- Trading Statistics
- Utility Impact (flash assists, damage)
- Positional Analysis
- Decision Making
- Overall Leetify Rating

## Project Structure

```
CS2Coach/
├── cmd/
│   └── main.go
├── internal/
│   ├── analyzer/
│   ├── coach/
│   ├── models/
│   ├── parser/
│   └── ml/
└── pkg/
```

## Dependencies

- [demoinfocs-golang](https://github.com/markus-wa/demoinfocs-golang) - CS2 demo parsing
- Ollama - Local LLM for coaching recommendations
- Standard Go libraries

## Contributing

Pull requests welcome! Please read our contributing guidelines and code of conduct before submitting PRs.

## License

This project is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for more details.

You should have received a copy of the GNU General Public License along with this program. If not, see <https://www.gnu.org/licenses/>.