# Calculator MCP Server - Installation Guide

## Quick Start

### Option 1: Using the setup script (Recommended)
```bash
cd calculator-mcp
python setup.py --all
```

This will:
1. Install all dependencies
2. Verify the installation
3. Run basic tests
4. Show usage instructions

### Option 2: Manual installation
```bash
cd calculator-mcp

# Install dependencies
pip install -r requirements.txt

# Verify installation
python setup.py --verify

# Run tests
python test_calculator.py
```

## System Requirements

### Python Version
- Python 3.8 or higher
- pip package manager

### Operating Systems
- ✅ Linux
- ✅ macOS
- ✅ Windows (with Python 3.8+)

### Dependencies
See `requirements.txt` for complete list:
- mcp[fastmcp]>=1.0.0
- pydantic>=2.0.0
- httpx>=0.25.0
- numpy>=1.24.0 (optional, for advanced numerical operations)

## Step-by-Step Installation

### 1. Clone or Download
```bash
# If you have the repository
cd calculator-mcp

# Or download the files manually
# Ensure you have all files from the calculator-mcp directory
```

### 2. Create Virtual Environment (Optional but Recommended)
```bash
# Create virtual environment
python -m venv venv

# Activate on Linux/macOS
source venv/bin/activate

# Activate on Windows
venv\Scripts\activate
```

### 3. Install Dependencies
```bash
pip install -r requirements.txt
```

### 4. Verify Installation
```bash
python setup.py --verify
```

Expected output:
```
Verifying installation...
✓ mcp
✓ pydantic
✓ httpx
✓ numpy

All packages installed successfully!
```

### 5. Run Tests
```bash
python test_calculator.py
```

Expected output shows successful test runs for all calculator tools.

## Running the Server

### Basic Usage
```bash
python calculator_mcp.py
```

The server will start and wait for MCP client connections via stdio transport.

### With MCP Inspector
```bash
# Install MCP Inspector globally (first time only)
npm install -g @modelcontextprotocol/inspector

# Run with inspector
npx @modelcontextprotocol/inspector python calculator_mcp.py
```

This opens a web interface at http://localhost:5173 where you can:
- See available tools
- Test tool calls
- View responses
- Debug issues

### Integration with MCP Clients

The server uses stdio transport, which is compatible with most MCP clients:

#### Claude Desktop
Add to `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "calculator": {
      "command": "python",
      "args": ["/full/path/to/calculator_mcp.py"]
    }
  }
}
```

#### Cursor
Configure in Cursor's MCP settings with the path to `calculator_mcp.py`.

#### Other Clients
Most MCP clients support stdio servers. Consult your client's documentation for how to add external MCP servers.

## Testing

### Run All Tests
```bash
python test_calculator.py
```

### Run Specific Tests
```bash
# Test basic operations only
python -c "
import asyncio
from test_calculator import test_basic_operations
asyncio.run(test_basic_operations())
"

# Test with custom values
python -c "
import asyncio
from calculator_mcp import calculator_basic_operation, BasicOperationInput, OperationType, ResponseFormat

async def test():
    params = BasicOperationInput(
        operation=OperationType.ADD,
        a=10,
        b=20,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_basic_operation(params)
    print(result)

asyncio.run(test())
"
```

### Example Usage
```bash
python example_usage.py
```

This demonstrates real-world usage scenarios including:
- Basic arithmetic
- Advanced mathematical functions
- Trigonometric calculations
- Statistical analysis
- Unit conversions
- Practical application examples

## Troubleshooting

### Common Issues

#### 1. "ModuleNotFoundError: No module named 'mcp'"
```bash
# Reinstall dependencies
pip install -r requirements.txt

# Or install manually
pip install mcp[fastmcp] pydantic httpx numpy
```

#### 2. "SyntaxError" when running Python 2.x
Ensure you're using Python 3.8 or higher:
```bash
python --version
# Should show Python 3.x.x

python3 --version
# Try python3 if python points to Python 2
```

#### 3. "Permission denied" when installing packages
```bash
# Use user install
pip install --user -r requirements.txt

# Or use virtual environment
python -m venv venv
source venv/bin/activate  # or venv\Scripts\activate on Windows
pip install -r requirements.txt
```

#### 4. MCP Inspector not connecting
```bash
# Check if server runs independently
python calculator_mcp.py
# Should start without errors

# Check Node.js version
node --version
# Should be 14.x or higher

# Reinstall MCP Inspector
npm uninstall -g @modelcontextprotocol/inspector
npm install -g @modelcontextprotocol/inspector
```

### Debug Mode

Enable debug logging by modifying `calculator_mcp.py`:
```python
if __name__ == "__main__":
    import logging
    logging.basicConfig(level=logging.DEBUG)
    mcp.run()
```

## Updating

### Update Dependencies
```bash
pip install --upgrade -r requirements.txt
```

### Check for Updates
```bash
python setup.py --verify
```

## Uninstallation

### Remove Dependencies
```bash
pip uninstall mcp pydantic httpx numpy -y
```

### Remove Virtual Environment
```bash
# If using virtual environment
deactivate  # Exit the environment
rm -rf venv  # Remove the directory
```

## Support

### Getting Help
1. Check the `README.md` for detailed documentation
2. Run `python setup.py --help` for command options
3. Review `example_usage.py` for usage patterns
4. Check the MCP documentation: https://modelcontextprotocol.io

### Reporting Issues
If you encounter issues:
1. Run `python validate.py` to check for common problems
2. Ensure all dependencies are installed correctly
3. Check Python version compatibility
4. Review error messages for specific details

## License

This project is licensed under the MIT License. See the `README.md` file for details.

## Acknowledgments

- Built with the MCP Python SDK (FastMCP)
- Uses Pydantic for data validation
- Follows MCP best practices and conventions