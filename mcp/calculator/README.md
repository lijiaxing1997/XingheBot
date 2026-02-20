# Calculator MCP Server

A Python MCP server that provides comprehensive calculator tools for mathematical operations.

## Features

### Basic Arithmetic Operations
- Addition, subtraction, multiplication, division
- Proper error handling for division by zero

### Advanced Mathematical Functions
- Power (x^y)
- Square root (√x)
- Logarithm (log_b(x))
- Logarithm base 10 (log10(x))
- Exponential (e^x)
- Absolute value (|x|)
- Floor (⌊x⌋)
- Ceil (⌈x⌉)

### Trigonometric Functions
- Sine, cosine, tangent
- Arcsine, arccosine, arctangent
- Support for both degrees and radians

### Statistical Calculations
- Mean, median, mode
- Standard deviation, variance
- Minimum, maximum, sum

### Unit Conversions
- Temperature: Celsius, Fahrenheit, Kelvin
- Length: meter, kilometer, centimeter, millimeter, inch, foot, yard, mile
- Weight: kilogram, gram, pound, ounce

## Installation

### Quick Setup
```bash
# Navigate to mcp/calculator directory
cd mcp/calculator

# Run setup script
python setup.py --all
```

### Manual Installation
```bash
# Install dependencies
pip install -r requirements.txt

# Verify installation
python setup.py --verify

# Run tests
python test_calculator.py
```

## Usage

### Running the MCP Server
```bash
python calculator_mcp.py
```

### Using with MCP Inspector
```bash
# Install MCP Inspector (if not already installed)
npm install -g @modelcontextprotocol/inspector

# Run with inspector
npx @modelcontextprotocol/inspector python calculator_mcp.py
```

### Direct Tool Testing
```bash
# Run the test script to see examples
python test_calculator.py
```

## Tools Available

### 1. **calculator_basic_operation**
Perform basic arithmetic operations: addition, subtraction, multiplication, division.

**Parameters:**
- `operation`: "add", "subtract", "multiply", "divide"
- `a`: First operand (float)
- `b`: Second operand (float)
- `response_format`: "markdown" or "json" (default: "markdown")

### 2. **calculator_advanced_math**
Perform advanced mathematical functions.

**Parameters:**
- `operation`: "power", "square_root", "logarithm", "logarithm10", "exponential", "absolute", "floor", "ceil"
- `value`: Input value (float)
- `exponent`: Exponent for power operation (default: 2)
- `base`: Base for logarithm (default: e)
- `response_format`: "markdown" or "json" (default: "markdown")

### 3. **calculator_trigonometric**
Perform trigonometric functions.

**Parameters:**
- `operation`: "sine", "cosine", "tangent", "arcsine", "arccosine", "arctangent"
- `angle`: Angle in degrees or value for inverse functions (float)
- `use_radians`: Whether angle is in radians (default: false)
- `response_format`: "markdown" or "json" (default: "markdown")

### 4. **calculator_statistics**
Perform statistical calculations.

**Parameters:**
- `operation`: "mean", "median", "mode", "stdev", "variance", "min", "max", "sum"
- `values`: List of numerical values
- `response_format`: "markdown" or "json" (default: "markdown")

### 5. **calculator_unit_conversion**
Convert between different units.

**Parameters:**
- `unit_type`: "temperature", "length", "weight"
- `value`: Value to convert (float)
- `from_unit`: Source unit
- `to_unit`: Target unit
- `response_format`: "markdown" or "json" (default: "markdown")

## Examples

### Basic Arithmetic
```python
# Addition
calculator_basic_operation({
    "operation": "add",
    "a": 5,
    "b": 3
})

# Division with JSON output
calculator_basic_operation({
    "operation": "divide",
    "a": 15,
    "b": 3,
    "response_format": "json"
})
```

### Advanced Math
```python
# Square root
calculator_advanced_math({
    "operation": "square_root",
    "value": 16
})

# Logarithm base 2
calculator_advanced_math({
    "operation": "logarithm",
    "value": 8,
    "base": 2
})
```

### Unit Conversion
```python
# Celsius to Fahrenheit
calculator_unit_conversion({
    "unit_type": "temperature",
    "value": 100,
    "from_unit": "celsius",
    "to_unit": "fahrenheit"
})

# Meters to inches
calculator_unit_conversion({
    "unit_type": "length",
    "value": 1,
    "from_unit": "meter",
    "to_unit": "inch",
    "response_format": "json"
})
```

## Development

### Project Structure
```
mcp/calculator/
├── calculator_mcp.py      # Main MCP server implementation
├── test_calculator.py     # Test script
├── setup.py              # Setup and installation script
├── requirements.txt      # Python dependencies
└── README.md            # This file
```

### Running Tests
```bash
# Run all tests
python test_calculator.py

# Run specific test functions
python -c "import asyncio; from test_calculator import test_basic_operations; asyncio.run(test_basic_operations())"
```

### Code Quality
- Uses Pydantic v2 for input validation
- Follows MCP best practices
- Comprehensive error handling
- Support for both markdown and JSON output formats
- Async/await for all operations

## License

MIT License

Copyright (c) 2024 Calculator MCP Server

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.