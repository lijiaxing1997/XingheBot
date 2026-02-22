#!/usr/bin/env python3
'''
Hash Calculator Module

This module provides hash calculation functionality for the MCP calculator server.
It supports multiple hash algorithms, input types, and normalization features.
'''

import hashlib
import json
import os
import re
from typing import Optional, List, Dict, Any, Union, Literal
from enum import Enum
from pathlib import Path
from pydantic import BaseModel, Field, field_validator, ConfigDict
import zlib
import base64

# Enums for hash algorithms and input types
class HashAlgorithm(str, Enum):
    '''Supported hash algorithms.'''
    MD5 = "md5"
    SHA1 = "sha1"
    SHA256 = "sha256"
    SHA512 = "sha512"
    SHA3_256 = "sha3_256"
    SHA3_512 = "sha3_512"
    BLAKE2B = "blake2b"
    CRC32 = "crc32"

class InputType(str, Enum):
    '''Supported input types.'''
    TEXT = "text"
    FILE = "file"
    BASE64 = "base64"
    HEX = "hex"

class NormalizationType(str, Enum):
    '''Supported normalization types.'''
    NONE = "none"
    JSON = "json"
    TEXT = "text"

class ResponseFormat(str, Enum):
    '''Output format for tool responses.'''
    MARKDOWN = "markdown"
    JSON = "json"

# Pydantic Models for Input Validation
class HashCalculationInput(BaseModel):
    '''Input model for hash calculations.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    algorithm: HashAlgorithm = Field(..., description="Hash algorithm to use")
    input_type: InputType = Field(..., description="Type of input data")
    input_data: str = Field(..., description="Input data (text, file path, base64, or hex)")
    normalization: NormalizationType = Field(default=NormalizationType.NONE, description="Normalization to apply before hashing")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('input_data')
    @classmethod
    def validate_input_data(cls, v: str, info) -> str:
        input_type = info.data.get('input_type')
        
        if input_type == InputType.FILE:
            # Check if file exists
            if not os.path.exists(v):
                raise ValueError(f"File not found: {v}")
            if not os.path.isfile(v):
                raise ValueError(f"Path is not a file: {v}")
        elif input_type == InputType.BASE64:
            # Validate base64 encoding
            try:
                base64.b64decode(v, validate=True)
            except Exception:
                raise ValueError("Invalid base64 encoded data")
        elif input_type == InputType.HEX:
            # Validate hex encoding
            if not re.match(r'^[0-9a-fA-F]+$', v):
                raise ValueError("Invalid hex encoded data")
        
        return v

class HashComparisonInput(BaseModel):
    '''Input model for hash comparisons.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    algorithm: HashAlgorithm = Field(..., description="Hash algorithm to use")
    input_type: InputType = Field(..., description="Type of input data")
    input_data: str = Field(..., description="Input data (text, file path, base64, or hex)")
    expected_hash: str = Field(..., description="Expected hash value to compare against")
    normalization: NormalizationType = Field(default=NormalizationType.NONE, description="Normalization to apply before hashing")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('expected_hash')
    @classmethod
    def validate_expected_hash(cls, v: str) -> str:
        # Remove any whitespace and convert to lowercase
        v = v.strip().lower()
        if not re.match(r'^[0-9a-f]+$', v):
            raise ValueError("Expected hash must be a hexadecimal string")
        return v

# Normalization functions
def normalize_json(data: str) -> bytes:
    '''Normalize JSON data by sorting keys and removing whitespace.'''
    try:
        parsed = json.loads(data)
        normalized = json.dumps(parsed, sort_keys=True, separators=(',', ':'))
        return normalized.encode('utf-8')
    except json.JSONDecodeError as e:
        raise ValueError(f"Invalid JSON data: {str(e)}")

def normalize_text(data: str) -> bytes:
    '''Normalize text data by removing extra whitespace and normalizing line endings.'''
    # Remove leading/trailing whitespace
    data = data.strip()
    # Normalize line endings to \n
    data = re.sub(r'\r\n|\r', '\n', data)
    # Remove multiple consecutive newlines
    data = re.sub(r'\n{3,}', '\n\n', data)
    # Remove multiple consecutive spaces
    data = re.sub(r' {2,}', ' ', data)
    return data.encode('utf-8')

def normalize_data(data: bytes, normalization: NormalizationType) -> bytes:
    '''Apply normalization to data based on the specified type.'''
    if normalization == NormalizationType.NONE:
        return data
    
    data_str = data.decode('utf-8', errors='ignore')
    
    if normalization == NormalizationType.JSON:
        return normalize_json(data_str)
    elif normalization == NormalizationType.TEXT:
        return normalize_text(data_str)
    else:
        return data

# Hash calculation functions
def calculate_hash(data: bytes, algorithm: HashAlgorithm) -> str:
    '''Calculate hash of data using the specified algorithm.'''
    if algorithm == HashAlgorithm.MD5:
        return hashlib.md5(data).hexdigest()
    elif algorithm == HashAlgorithm.SHA1:
        return hashlib.sha1(data).hexdigest()
    elif algorithm == HashAlgorithm.SHA256:
        return hashlib.sha256(data).hexdigest()
    elif algorithm == HashAlgorithm.SHA512:
        return hashlib.sha512(data).hexdigest()
    elif algorithm == HashAlgorithm.SHA3_256:
        return hashlib.sha3_256(data).hexdigest()
    elif algorithm == HashAlgorithm.SHA3_512:
        return hashlib.sha3_512(data).hexdigest()
    elif algorithm == HashAlgorithm.BLAKE2B:
        return hashlib.blake2b(data).hexdigest()
    elif algorithm == HashAlgorithm.CRC32:
        # CRC32 returns an integer, convert to hex
        crc_value = zlib.crc32(data) & 0xffffffff
        return f"{crc_value:08x}"
    else:
        raise ValueError(f"Unsupported algorithm: {algorithm}")

def get_input_data(input_type: InputType, input_data: str) -> bytes:
    '''Get bytes from input based on input type.'''
    if input_type == InputType.TEXT:
        return input_data.encode('utf-8')
    elif input_type == InputType.FILE:
        with open(input_data, 'rb') as f:
            return f.read()
    elif input_type == InputType.BASE64:
        return base64.b64decode(input_data)
    elif input_type == InputType.HEX:
        return bytes.fromhex(input_data)
    else:
        raise ValueError(f"Unsupported input type: {input_type}")

# Formatting functions
def _format_result_markdown(operation: str, result: Union[str, Dict[str, Any]], details: Dict[str, Any] = None) -> str:
    '''Format result as markdown.'''
    lines = [f"# {operation}", ""]
    
    if details:
        for key, value in details.items():
            if isinstance(value, (list, dict)):
                lines.append(f"- **{key.replace('_', ' ').title()}**:")
                if isinstance(value, list):
                    for item in value:
                        lines.append(f"  - {item}")
                else:
                    for k, v in value.items():
                        lines.append(f"  - **{k}**: {v}")
            elif isinstance(value, float):
                lines.append(f"- **{key.replace('_', ' ').title()}**: {value:.6f}")
            else:
                lines.append(f"- **{key.replace('_', ' ').title()}**: {value}")
        lines.append("")
    
    if isinstance(result, dict):
        lines.append("## Results:")
        for key, value in result.items():
            lines.append(f"- **{key}**: {value}")
    else:
        lines.append(f"## Result: **{result}**")
    
    return "\n".join(lines)

def _format_result_json(operation: str, result: Union[str, Dict[str, Any]], details: Dict[str, Any] = None) -> str:
    '''Format result as JSON.'''
    import json
    response = {
        "operation": operation,
        "result": result,
        "details": details or {}
    }
    return json.dumps(response, indent=2)

# Main hash calculation function
def calculate_hash_wrapper(params: HashCalculationInput) -> str:
    '''Calculate hash of input data with optional normalization.'''
    try:
        # Get input data
        data = get_input_data(params.input_type, params.input_data)
        
        # Apply normalization if requested
        normalized_data = normalize_data(data, params.normalization)
        
        # Calculate hash
        hash_value = calculate_hash(normalized_data, params.algorithm)
        
        # Prepare details
        details = {
            "algorithm": params.algorithm.value,
            "input_type": params.input_type.value,
            "normalization": params.normalization.value,
            "input_size_bytes": len(data),
            "normalized_size_bytes": len(normalized_data) if params.normalization != NormalizationType.NONE else len(data)
        }
        
        if params.input_type == InputType.FILE:
            details["file_path"] = params.input_data
            details["file_size"] = os.path.getsize(params.input_data)
        
        # Format response
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Hash Calculation: {params.algorithm.value}", hash_value, details)
        else:
            return _format_result_json(f"Hash Calculation: {params.algorithm.value}", hash_value, details)
            
    except Exception as e:
        return f"Error: {str(e)}"

# Hash comparison function
def compare_hash_wrapper(params: HashComparisonInput) -> str:
    '''Calculate hash of input data and compare with expected hash.'''
    try:
        # Get input data
        data = get_input_data(params.input_type, params.input_data)
        
        # Apply normalization if requested
        normalized_data = normalize_data(data, params.normalization)
        
        # Calculate hash
        calculated_hash = calculate_hash(normalized_data, params.algorithm)
        
        # Compare with expected hash
        match = calculated_hash.lower() == params.expected_hash.lower()
        
        # Prepare details
        details = {
            "algorithm": params.algorithm.value,
            "input_type": params.input_type.value,
            "normalization": params.normalization.value,
            "calculated_hash": calculated_hash,
            "expected_hash": params.expected_hash,
            "match": match,
            "input_size_bytes": len(data),
            "normalized_size_bytes": len(normalized_data) if params.normalization != NormalizationType.NONE else len(data)
        }
        
        if params.input_type == InputType.FILE:
            details["file_path"] = params.input_data
            details["file_size"] = os.path.getsize(params.input_data)
        
        # Prepare result
        result = {
            "match": match,
            "calculated_hash": calculated_hash,
            "expected_hash": params.expected_hash
        }
        
        # Format response
        if params.response_format == ResponseFormat.MARKDOWN:
            operation = f"Hash Comparison: {params.algorithm.value}"
            if match:
                operation += " ✓ MATCH"
            else:
                operation += " ✗ MISMATCH"
            return _format_result_markdown(operation, result, details)
        else:
            return _format_result_json(f"Hash Comparison: {params.algorithm.value}", result, details)
            
    except Exception as e:
        return f"Error: {str(e)}"

# Batch hash calculation function
def batch_hash_calculation(
    algorithm: HashAlgorithm,
    input_type: InputType,
    input_list: List[str],
    normalization: NormalizationType = NormalizationType.NONE,
    response_format: ResponseFormat = ResponseFormat.MARKDOWN
) -> str:
    '''Calculate hashes for multiple inputs.'''
    try:
        results = []
        details_list = []
        
        for input_data in input_list:
            # Get input data
            data = get_input_data(input_type, input_data)
            
            # Apply normalization if requested
            normalized_data = normalize_data(data, normalization)
            
            # Calculate hash
            hash_value = calculate_hash(normalized_data, algorithm)
            
            # Prepare result entry
            result_entry = {
                "input": input_data,
                "hash": hash_value
            }
            
            if input_type == InputType.FILE:
                result_entry["file_size"] = os.path.getsize(input_data)
            
            results.append(result_entry)
            
            # Prepare details entry
            details_entry = {
                "input": input_data,
                "algorithm": algorithm.value,
                "normalization": normalization.value,
                "input_size_bytes": len(data),
                "normalized_size_bytes": len(normalized_data) if normalization != NormalizationType.NONE else len(data)
            }
            
            if input_type == InputType.FILE:
                details_entry["file_path"] = input_data
                details_entry["file_size"] = os.path.getsize(input_data)
            
            details_list.append(details_entry)
        
        # Prepare overall details
        overall_details = {
            "algorithm": algorithm.value,
            "input_type": input_type.value,
            "normalization": normalization.value,
            "total_inputs": len(input_list),
            "inputs": details_list
        }
        
        # Format response
        if response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Batch Hash Calculation: {algorithm.value}", results, overall_details)
        else:
            return _format_result_json(f"Batch Hash Calculation: {algorithm.value}", results, overall_details)
            
    except Exception as e:
        return f"Error: {str(e)}"

# Example usage and testing
if __name__ == "__main__":
    # Test hash calculation
    print("=== Testing Hash Calculation ===")
    
    # Test with text input
    params = HashCalculationInput(
        algorithm=HashAlgorithm.SHA256,
        input_type=InputType.TEXT,
        input_data="Hello, World!",
        normalization=NormalizationType.NONE,
        response_format=ResponseFormat.MARKDOWN
    )
    
    result = calculate_hash_wrapper(params)
    print("SHA256 of 'Hello, World!':")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test with JSON normalization
    params = HashCalculationInput(
        algorithm=HashAlgorithm.SHA256,
        input_type=InputType.TEXT,
        input_data='{"b": 2, "a": 1, "c": 3}',
        normalization=NormalizationType.JSON,
        response_format=ResponseFormat.JSON
    )
    
    result = calculate_hash_wrapper(params)
    print("SHA256 of normalized JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test hash comparison
    print("=== Testing Hash Comparison ===")
    
    params = HashComparisonInput(
        algorithm=HashAlgorithm.SHA256,
        input_type=InputType.TEXT,
        input_data="Hello, World!",
        expected_hash="dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f",
        normalization=NormalizationType.NONE,
        response_format=ResponseFormat.MARKDOWN
    )
    
    result = compare_hash_wrapper(params)
    print("Hash comparison (should match):")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()