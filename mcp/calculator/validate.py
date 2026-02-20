#!/usr/bin/env python3
'''
Simple validation script for Calculator MCP Server

This script validates the Python syntax and basic imports
without actually running async code.
'''

import ast
import sys
import os

def validate_python_syntax(filepath):
    '''Validate Python file syntax.'''
    print(f"Validating {filepath}...")
    
    try:
        with open(filepath, 'r', encoding='utf-8') as f:
            content = f.read()
        
        # Parse the AST to check syntax
        ast.parse(content)
        print(f"  ✓ Syntax is valid")
        return True
    except SyntaxError as e:
        print(f"  ✗ Syntax error: {e}")
        return False
    except Exception as e:
        print(f"  ✗ Error reading file: {e}")
        return False

def check_imports():
    '''Check if required imports are valid.'''
    print("\nChecking imports...")
    
    # Check standard library imports
    std_imports = ['typing', 'enum', 'math', 'statistics']
    for imp in std_imports:
        try:
            __import__(imp)
            print(f"  ✓ {imp}")
        except ImportError:
            print(f"  ✗ {imp} (not found)")
    
    # Check third-party imports
    third_party = ['pydantic', 'mcp']
    for imp in third_party:
        try:
            __import__(imp)
            print(f"  ✓ {imp}")
        except ImportError:
            print(f"  ✗ {imp} (not installed - run: pip install {imp})")

def check_file_structure():
    '''Check that all required files exist.'''
    print("\nChecking file structure...")
    
    required_files = [
        'calculator_mcp.py',
        'requirements.txt',
        'README.md'
    ]
    
    optional_files = [
        'test_calculator.py',
        'setup.py',
        'example_usage.py'
    ]
    
    for file in required_files:
        if os.path.exists(file):
            print(f"  ✓ {file}")
        else:
            print(f"  ✗ {file} (missing)")
    
    print("\nOptional files:")
    for file in optional_files:
        if os.path.exists(file):
            print(f"  ✓ {file}")
        else:
            print(f"  - {file} (not required)")

def main():
    '''Main validation function.'''
    print("Calculator MCP Server - Validation Check")
    print("=" * 50)
    
    # Change to script directory
    script_dir = os.path.dirname(os.path.abspath(__file__))
    os.chdir(script_dir)
    
    # Validate main files
    main_files = ['calculator_mcp.py', 'test_calculator.py', 'setup.py', 'example_usage.py']
    syntax_ok = True
    
    for file in main_files:
        if os.path.exists(file):
            if not validate_python_syntax(file):
                syntax_ok = False
    
    # Check imports
    check_imports()
    
    # Check file structure
    check_file_structure()
    
    # Summary
    print("\n" + "=" * 50)
    print("Validation Summary:")
    
    if syntax_ok:
        print("✓ All Python files have valid syntax")
    else:
        print("✗ Some files have syntax errors")
    
    print("\nNext steps:")
    print("1. Install dependencies: pip install -r requirements.txt")
    print("2. Run tests: python test_calculator.py")
    print("3. Start server: python calculator_mcp.py")
    print("4. Test with MCP Inspector: npx @modelcontextprotocol/inspector python calculator_mcp.py")

if __name__ == "__main__":
    main()